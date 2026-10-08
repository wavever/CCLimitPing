package cli

// The `continue` proxy launches a provider's interactive CLI under limitping and
// passes the terminal straight through, so the user drives Codex/Claude Code
// exactly as normal. In the background limitping polls usage and, when the 5h
// limit recovers after being hit, injects the configured continue message so a
// long task resumes itself instead of sitting parked at the limit.
//
// This file holds the platform-neutral decision logic, the usage loop, the
// on-screen limit-message detector, and a diagnostic log; the PTY and
// raw-terminal plumbing lives in proxy_unix.go (with a Windows stub).

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/notify"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

// newContinueCmd is the standalone `limitping continue <provider>` command: it
// proxies the provider's interactive CLI and auto-injects the continue message
// when the 5h limit recovers. It is separate from `watch` (the headless ping
// daemon) because it needs a live terminal and the user drives the session.
//
// Flags after the provider (e.g. `continue codex --yolo`) are NOT limitping
// flags — they pass straight through to the launched CLI.
func newContinueCmd() *cobra.Command {
	text := localizedText()
	cmd := &cobra.Command{
		Use:       "continue <provider> [cli args...]",
		Short:     text.continueShort,
		Long:      text.continueLong,
		Args:      cobra.MinimumNArgs(1),
		ValidArgs: []string{"claude", "codex"},
		RunE: func(cmd *cobra.Command, args []string) error {
			updateNotice(cmd.Context(), cmd.OutOrStdout(), text, os.Stdin)
			providerName := args[0]
			if providerName != "claude" && providerName != "codex" {
				return fmt.Errorf("%s %q", text.continueBadProvider, providerName)
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return runContinueProxy(cmd.Context(), cmd.OutOrStdout(), providerName, args[1:], cfg)
		},
	}
	// Stop parsing limitping's own flags at the first positional (the provider),
	// so everything after it is forwarded verbatim to the provider's CLI.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

const (
	// proxyPoll is the usage poll cadence. It is deliberately short and fixed
	// (not "sleep until the reset time") so the loop stays robust to system
	// sleep: macOS pauses the monotonic clock while asleep, so a single long
	// timer set before sleep would not fire for hours after waking. A 1-minute
	// zero-quota GET re-checks promptly once the machine is awake again.
	proxyPoll         = time.Minute
	proxyReadTimeout  = 30 * time.Second
	proxyInjectSettle = 300 * time.Millisecond
	proxyAskSettle    = 300 * time.Millisecond
	proxyAskCooldown  = 30 * time.Minute

	// proxyScanThrottle rate-limits the output detector's tail rescans: TUIs
	// redraw many times a second, and anything still on screen (the limit
	// banner, the limit question) keeps being repainted, so a skipped chunk is
	// picked up by the next scan at worst proxyScanThrottle later.
	proxyScanThrottle = 200 * time.Millisecond

	// limitSignalFreshFor bounds how long an on-screen limit message keeps
	// counting as evidence. The banner is only a corroborating fallback for
	// endpoint lag, which resolves within minutes; without decay a stray
	// "rate limit" in ordinary output would arm the parked state forever.
	limitSignalFreshFor = 10 * time.Minute

	// limitHighPct: at/above this the 5h window is treated as effectively maxed
	// (the endpoint may report e.g. 99.x at the cap, never exactly 100).
	// limitLowPct: once parked, dropping below this means the window has reset.
	limitHighPct = 95.0
	limitLowPct  = 50.0
)

// claudeSelfResumeGrace bounds how long the proxy waits for Claude Code to
// resume a task on its own before typing the message itself. Claude Code
// resumes at the reset time plus a small jitter, and the proxy only notices
// the reset on its next poll, so it has usually already happened. Variables so
// tests can shorten them, along with the session list they consult.
var (
	claudeSelfResumeGrace = 90 * time.Second
	claudeSelfResumePoll  = 5 * time.Second
	claudeSessions        = provider.ClaudeSessions
)

// continueArmer tracks whether the session is parked at the 5h limit and fires
// once on the recovery edge. "Parked" is set from the usage endpoint (5h maxed
// or limit_reached) or, as a corroborated fallback, the on-screen limit message
// while the 5h window is still meaningfully used. It only fires when no limit
// is currently reported (limit_reached can be true with the 5h window low, e.g.
// a weekly cap — firing then would inject on every poll), the 5h window has
// clearly reset (high → low), and the weekly window isn't exhausted per the
// same threshold+credits rule the scheduler uses (continuing into a spent
// weekly window would just hit the wall again).
type continueArmer struct {
	weeklyThreshold float64 // 0..1, cfg.WeeklyThreshold
	parked          bool
}

// observe folds in the latest usage snapshot (and whether the CLI has shown a
// limit message) and reports whether to inject the continue message now.
func (a *continueArmer) observe(u *usage.Usage, sawLimitMsg bool) bool {
	fh := u.FiveHour.UsedPercent
	if fh >= limitHighPct || u.LimitReached || (sawLimitMsg && fh >= limitLowPct) {
		a.parked = true
	}
	if a.parked && !u.LimitReached && fh < limitLowPct && !u.WeeklyExhausted(a.weeklyThreshold) {
		a.parked = false
		return true
	}
	return false
}

// watchAndContinue polls the provider's usage and types the continue message
// into the proxied child whenever the 5h limit recovers after being hit. It
// runs until ctx is cancelled (i.e. the proxied CLI exits). det carries the
// on-screen limit-message signal from the output detector; lg records a
// diagnostic timeline (consecutive identical poll/error lines are collapsed so
// a parked overnight session doesn't grow the log by a line per minute).
//
// selfResumed, when set, is asked at the recovery edge whether the CLI resumed
// the task on its own; the message is only typed when it did not, so the task
// is never continued twice.
func watchAndContinue(ctx context.Context, p provider.Provider, inject *sessionInjector, msg string, cfg config.Config, det *limitDetector, lg *proxyLogger, selfResumed func(context.Context) bool) {
	lg.logf("watcher: polling %s usage every %s", p.Name(), proxyPoll)
	armer := &continueArmer{weeklyThreshold: cfg.WeeklyThreshold}
	autoRedeem := providerConfig(cfg, p.Name()).AutoRedeem
	lastLine := ""
	logState := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if line != lastLine {
			lg.logf("%s", line)
			lastLine = line
		}
	}
	for {
		rctx, cancel := context.WithTimeout(ctx, proxyReadTimeout)
		// Only a loop that auto-redeems lets its reads carry the reset credits.
		u, err := provider.ReadUsageForLoop(rctx, p, autoRedeem)
		cancel()
		wasParked := armer.parked

		switch {
		case err != nil:
			logState("usage read error: %v", err)
		// A spent credit resets the windows, so u is stale: skip the rest of
		// this cycle and decide from the next poll.
		case autoRedeem && redeemExpiringCredit(ctx, p, u, lg):
			lastLine = ""
		case !armer.observe(u, det.seen()):
			if armer.parked && !wasParked {
				// Whatever Claude Code printed about resuming belongs to an
				// earlier limit, or was never about one.
				det.forgetNative()
			}
			logState("poll 5h=%.0f%% weekly=%.0f%% limit_reached=%t parked=%t saw_limit_msg=%t",
				u.FiveHour.UsedPercent, u.Weekly.UsedPercent, u.LimitReached, armer.parked, det.seen())
		case selfResumed != nil && selfResumed(ctx):
			lg.logf("RECOVERED 5h=%.0f%% weekly=%.0f%% — %s resumed the task itself; not injecting", u.FiveHour.UsedPercent, u.Weekly.UsedPercent, p.Name())
			lastLine = ""
			det.reset()
		default:
			lg.logf("RECOVERED 5h=%.0f%% weekly=%.0f%% — injecting %q", u.FiveHour.UsedPercent, u.Weekly.UsedPercent, msg)
			lastLine = ""
			inject.typeAndSubmit(msg, proxyInjectSettle)
			det.reset()
			if cfg.Notify {
				notify.Notify(p.Name()+": 5h limit recovered", "Sent “"+msg+"” to resume the task")
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(proxyPoll):
		}
	}
}

// claudeSelfResumed waits, at the recovery edge, for Claude Code to resume the
// task by itself, and reports whether it did. Claude Code continues a task
// stopped by a usage limit on its own once the limit resets (on by default,
// and armed by the "wait" choice of its limit dialog, which the proxy picks),
// so typing the continue message on top of that would run the task twice.
//
// It counts as resumed when Claude Code announces it on screen, or when its
// own session list shows this session mid-turn. Without either within
// claudeSelfResumeGrace — or once Claude Code says it will not resume — the
// proxy types the message as it always has; that is also what happens with a
// Claude Code too old to resume on its own, merely a little later.
func claudeSelfResumed(ctx context.Context, det *limitDetector, pid int, lg *proxyLogger) bool {
	deadline := time.Now().Add(claudeSelfResumeGrace)
	lg.logf("recovered: waiting up to %s for Claude Code to resume the task itself", claudeSelfResumeGrace)
	for {
		if det.nativeDeclined() {
			return false
		}
		if det.nativeContinued() {
			return true
		}
		if sessions, err := claudeSessions(ctx); err == nil {
			for _, s := range sessions {
				if s.PID == pid && s.Busy() {
					return true
				}
			}
		}
		if !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return true // the session is gone; there is nothing to type into
		case <-time.After(claudeSelfResumePoll):
		}
	}
}

// redeemExpiringCredit spends a banked reset credit that is about to lapse.
// It reports whether the rate-limit windows were actually reset; the provider
// throttles its own attempts, so calling this every poll is fine.
func redeemExpiringCredit(ctx context.Context, p provider.Provider, u *usage.Usage, lg *proxyLogger) bool {
	redeemer, ok := p.(provider.ResetCreditRedeemer)
	if !ok {
		return false
	}
	rctx, cancel := context.WithTimeout(ctx, proxyReadTimeout)
	res, err := redeemer.AutoRedeemResetCredit(rctx, u)
	cancel()
	switch {
	case err != nil:
		lg.logf("reset credit redeem failed: %v", err)
	case res.Outcome == provider.RedeemReset:
		lg.logf("REDEEMED an expiring reset credit — rate-limit windows reset")
		return true
	case res.Outcome != "":
		lg.logf("reset credit not spent: %s", res)
	}
	return false
}

// sessionInjector serializes synthetic keystrokes into the proxied child's PTY:
// the recovery message and the Claude limit-question confirmation can fire
// around the same moment from different goroutines, and interleaving them would
// submit a half-typed message.
type sessionInjector struct {
	mu sync.Mutex
	w  io.Writer
}

// typeAndSubmit types msg and, after a settle pause so the TUI registers the
// text, submits it with Enter. The lock is held across the pause on purpose so
// no other synthetic keystroke lands between the message and its Enter.
func (s *sessionInjector) typeAndSubmit(msg string, settle time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = io.WriteString(s.w, msg)
	time.Sleep(settle)
	_, _ = io.WriteString(s.w, "\r")
}

// submit presses Enter, accepting whatever the TUI currently highlights.
func (s *sessionInjector) submit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = io.WriteString(s.w, "\r")
}

// continueMessage is the per-provider message injected on recovery, defaulting
// to "continue" when unset.
func continueMessage(cfg config.Config, providerName string) string {
	var m string
	switch providerName {
	case "claude":
		m = cfg.Claude.ContinuePrompt
	case "codex":
		m = cfg.Codex.ContinuePrompt
	}
	if m == "" {
		return "continue"
	}
	return m
}

// limitSignal is the thread-safe, time-decaying flag the output detector raises
// when the CLI prints a usage/rate-limit message, read by the usage loop. It
// stores the raise time (unix nanos; 0 = clear) and stops counting as seen once
// the message hasn't been on screen for a while, so a stray phrase in ordinary
// output can't arm the parked state hours later.
type limitSignal struct {
	raisedAt atomic.Int64
	fresh    time.Duration // 0 = limitSignalFreshFor; tests shorten it
}

func (s *limitSignal) seen() bool {
	if s == nil {
		return false
	}
	t := s.raisedAt.Load()
	if t == 0 {
		return false
	}
	fresh := s.fresh
	if fresh == 0 {
		fresh = limitSignalFreshFor
	}
	return time.Since(time.Unix(0, t)) <= fresh
}

func (s *limitSignal) set() {
	if s != nil {
		s.raisedAt.Store(time.Now().UnixNano())
	}
}

func (s *limitSignal) clear() {
	if s != nil {
		s.raisedAt.Store(0)
	}
}

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// limitPhrases are matched case-insensitively against the CLI's output (with
// ANSI styling stripped) to recognize that a turn was blocked by a limit.
var limitPhrases = [][]byte{
	[]byte("session limit"),
	[]byte("usage limit"),
	[]byte("rate limit"),
	[]byte("limit reached"),
	[]byte("reached your limit"),
}

// limitDetector is an io.Writer spliced into the child→terminal stream: it scans
// a rolling tail of the output and raises sig when a limit message appears.
// Scans are throttled (scanEvery) because this sits on the terminal output path
// and TUIs redraw many times a second; anything still on screen keeps being
// repainted, so a skipped chunk is picked up by the next scan.
type limitDetector struct {
	sig            *limitSignal
	lg             *proxyLogger
	claudeQuestion *claudeLimitQuestionConfirmer
	scanEvery      time.Duration // min interval between scans; 0 = every write (tests)

	mu       sync.Mutex
	buf      []byte
	lastScan time.Time

	nativeContinuedSeen atomic.Bool
	nativeDeclinedSeen  atomic.Bool
}

func (d *limitDetector) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.buf = append(d.buf, p...)
	if len(d.buf) > 8192 {
		d.buf = d.buf[len(d.buf)-8192:]
	}
	if d.scanEvery > 0 && time.Since(d.lastScan) < d.scanEvery {
		return len(p), nil
	}
	d.lastScan = time.Now()
	clean := cleanTerminalOutput(d.buf)
	if containsLimitPhraseClean(clean) {
		if !d.sig.seen() {
			d.lg.logf("output: detected a limit message on screen")
		}
		d.sig.set() // re-raise while the banner stays on screen, keeping it fresh
	}
	if d.claudeQuestion.shouldScan() && containsClaudeLimitQuestionClean(clean) {
		d.claudeQuestion.confirm()
	}
	compact := compactTerminalText(clean)
	if nativeContinuedRE.Match(compact) && !d.nativeContinuedSeen.Swap(true) {
		d.lg.logf("output: Claude Code is continuing the task on its own")
	}
	if nativeDeclinedRE.Match(compact) && !d.nativeDeclinedSeen.Swap(true) {
		d.lg.logf("output: Claude Code will not continue the task on its own")
	}
	return len(p), nil
}

// seen is the watcher's view of the on-screen limit signal.
func (d *limitDetector) seen() bool { return d.sig.seen() }

// nativeContinued reports that Claude Code announced it is resuming the task
// itself since the last reset; nativeDeclined that it announced it will not.
// Both stick once seen — the announcement may scroll out of the scanned tail
// long before the watcher asks.
func (d *limitDetector) nativeContinued() bool { return d.nativeContinuedSeen.Load() }
func (d *limitDetector) nativeDeclined() bool  { return d.nativeDeclinedSeen.Load() }

// reset clears the signal after an injection, dropping the buffered tail too so
// the stale banner text can't immediately re-raise the signal it just cleared.
func (d *limitDetector) reset() {
	d.mu.Lock()
	d.buf = d.buf[:0]
	d.mu.Unlock()
	d.sig.clear()
	d.forgetNative()
}

// Claude Code (2.1.2xx+) can wait out a usage limit and continue the task by
// itself (the autoContinueAtUsageLimit setting, also what its limit dialog's
// "wait" choice arms). These match the lines it prints when it does —
// "Usage limit reset · continuing automatically", "Usage limit available
// again · continuing now" — and when it gives up on doing so ("… this task
// will not resume on its own"). Each pins the "usage limit" or "task" half too,
// because "continuing now" alone is just as likely in the model's own reply.
// They run on the text with all whitespace removed: the TUI often draws a
// space as a cursor move, which the ANSI stripping deletes outright.
var (
	nativeContinuedRE = regexp.MustCompile(`usagelimit(reset|hasreset|availableagain)\W{0,6}continuing(automatically|now)`)
	nativeDeclinedRE  = regexp.MustCompile(`thistask(will|did)notresumeonitsown`)
)

// compactTerminalText drops all whitespace from already-cleaned output.
func compactTerminalText(clean []byte) []byte {
	return bytes.Join(bytes.Fields(clean), nil)
}

// forgetNative drops what the detector saw of Claude Code's own resume lines.
// The watcher calls it when the session parks at a limit, so the decision at
// that limit's recovery rests only on what was printed since.
func (d *limitDetector) forgetNative() {
	d.nativeContinuedSeen.Store(false)
	d.nativeDeclinedSeen.Store(false)
}

func containsAnyClean(clean []byte, phrases [][]byte) bool {
	for _, ph := range phrases {
		if bytes.Contains(clean, ph) {
			return true
		}
	}
	return false
}

func containsLimitPhrase(b []byte) bool {
	return containsLimitPhraseClean(cleanTerminalOutput(b))
}

// containsLimitPhraseClean expects input already passed through
// cleanTerminalOutput (ANSI-stripped, lowercased).
func containsLimitPhraseClean(clean []byte) bool {
	return containsAnyClean(clean, limitPhrases)
}

// containsClaudeLimitQuestion recognizes Claude Code's blocking Ask User
// Question shown after the 5h cap: normal text input is unavailable until the
// user chooses between waiting for the limit reset and upgrading.
func containsClaudeLimitQuestion(b []byte) bool {
	return containsClaudeLimitQuestionClean(cleanTerminalOutput(b))
}

// containsClaudeLimitQuestionClean expects input already passed through
// cleanTerminalOutput (ANSI-stripped, lowercased).
func containsClaudeLimitQuestionClean(cleaned []byte) bool {
	clean := bytes.Join(bytes.Fields(cleaned), []byte(" "))
	hasAsk := bytes.Contains(clean, []byte("ask user question")) ||
		bytes.Contains(clean, []byte("askuserquestion"))
	hasWait := bytes.Contains(clean, []byte("continue waiting")) ||
		bytes.Contains(clean, []byte("wait until")) ||
		bytes.Contains(clean, []byte("继续等待")) ||
		bytes.Contains(clean, []byte("继续等到"))
	hasUpgrade := bytes.Contains(clean, []byte("upgrade to pro")) ||
		bytes.Contains(clean, []byte("pro plan")) ||
		bytes.Contains(clean, []byte("升级到 pro")) ||
		bytes.Contains(clean, []byte("升级 pro"))
	hasLimit := containsLimitPhraseClean(clean) ||
		bytes.Contains(clean, []byte("limit reset")) ||
		bytes.Contains(clean, []byte("limit resets")) ||
		bytes.Contains(clean, []byte("限制恢复")) ||
		bytes.Contains(clean, []byte("限额恢复"))
	return hasAsk && hasLimit && (hasWait || hasUpgrade)
}

func cleanTerminalOutput(b []byte) []byte {
	return bytes.ToLower(ansiEscapeRE.ReplaceAll(b, nil))
}

// claudeLimitQuestionConfirmer answers Claude Code's limit-specific question by
// accepting the default "keep waiting" option. Without this, the TUI stays in a
// choice picker and the later injected continue message cannot reach the normal
// prompt.
type claudeLimitQuestionConfirmer struct {
	inject   *sessionInjector
	lg       *proxyLogger
	settle   time.Duration
	cooldown time.Duration
	mu       sync.Mutex
	last     time.Time
}

// shouldScan reports whether scanning output for the limit question is worth it
// at all: false for providers without a confirmer and inside the
// post-confirmation cooldown, keeping the multi-phrase scan off the output path
// most of the time.
func (c *claudeLimitQuestionConfirmer) shouldScan() bool {
	if c == nil || c.inject == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last.IsZero() || time.Since(c.last) >= c.cooldownOrDefault()
}

func (c *claudeLimitQuestionConfirmer) cooldownOrDefault() time.Duration {
	if c.cooldown == 0 {
		return proxyAskCooldown
	}
	return c.cooldown
}

func (c *claudeLimitQuestionConfirmer) confirm() {
	if c == nil || c.inject == nil {
		return
	}
	now := time.Now()
	c.mu.Lock()
	settle := c.settle
	if settle == 0 {
		settle = proxyAskSettle
	}
	if !c.last.IsZero() && now.Sub(c.last) < c.cooldownOrDefault() {
		c.mu.Unlock()
		return
	}
	c.last = now
	c.mu.Unlock()

	go func() {
		time.Sleep(settle)
		c.inject.submit()
		c.lg.logf("output: confirmed Claude limit question with Enter")
	}()
}

// proxyLogger appends a timestamped diagnostic timeline to
// <config dir>/continue.log. A nil/zero logger is a no-op, so callers never
// need to nil-check. Output goes to a file (not the terminal) because the
// proxied TUI owns stdout.
type proxyLogger struct{ w io.Writer }

func newProxyLogger() *proxyLogger {
	dir, err := config.Dir()
	if err != nil {
		return &proxyLogger{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &proxyLogger{}
	}
	path := filepath.Join(dir, "continue.log")
	rotateLog(path, proxyLogMaxBytes)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return &proxyLogger{}
	}
	return &proxyLogger{w: f}
}

// proxyLogMaxBytes caps continue.log; every session appends to it, so without
// a cap a machine that runs `continue` daily grows it without bound.
const proxyLogMaxBytes = 1 << 20

// rotateLog moves path aside to path.1 (replacing an older one) once it has
// grown past max, so the log keeps the latest sessions and one previous batch.
func rotateLog(path string, max int64) {
	if info, err := os.Stat(path); err == nil && info.Size() > max {
		_ = os.Rename(path, path+".1")
	}
}

func (l *proxyLogger) logf(format string, args ...any) {
	if l == nil || l.w == nil {
		return
	}
	fmt.Fprintf(l.w, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

func (l *proxyLogger) close() {
	if l == nil {
		return
	}
	if c, ok := l.w.(io.Closer); ok {
		_ = c.Close()
	}
}
