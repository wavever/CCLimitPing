package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
	"github.com/creack/pty"

	"github.com/wavever/CCLimitPing/internal/activity"
	"github.com/wavever/CCLimitPing/internal/auth"
	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

const (
	codexDefaultBaseURL = "https://chatgpt.com/backend-api"
	codexChatGPTPath    = "/wham/usage"
	codexResetPath      = "/wham/rate-limit-reset-credits"
	codexConsumePath    = "/wham/rate-limit-reset-credits/consume"
	codexAPIPath        = "/api/codex/usage"
	codexUserAgent      = "limitping"
	sparkDefaultModel   = "gpt-5.3-codex-spark"
	codexTurnComplete   = "\x1b]9;"
	codexTurnScanLimit  = 4096

	// codexRedeemCooldown throttles the automatic redemption path so a
	// once-a-minute poll loop cannot re-attempt a refused redemption every cycle.
	codexRedeemCooldown = 15 * time.Minute

	codexTurnMaxWait = 45 * time.Second
	codexExitGrace   = 5 * time.Second
)

type codexInteractiveTiming struct {
	maxWait   time.Duration
	exitGrace time.Duration
}

var defaultCodexInteractiveTiming = codexInteractiveTiming{
	maxWait:   codexTurnMaxWait,
	exitGrace: codexExitGrace,
}

// Codex reads usage via the ChatGPT backend usage endpoint and triggers windows
// via the interactive, TTY-backed Codex CLI. Headless `codex exec` can consume
// tokens without anchoring the subscription-backed Codex window.
type Codex struct {
	cfg  config.ProviderConfig
	auth *auth.CodexAuth

	redeemMu   sync.Mutex
	lastRedeem time.Time // last automatic redemption attempt, for the cooldown
}

func NewCodex(cfg config.ProviderConfig) *Codex {
	return &Codex{
		cfg:  cfg,
		auth: auth.NewCodexAuth(),
	}
}

func (c *Codex) Name() string { return "codex" }

func (c *Codex) ActiveTask(ctx context.Context) (string, bool, error) {
	return codexActiveTask(ctx)
}

func (c *Codex) ReadUsage(ctx context.Context) (*usage.Usage, error) {
	body, r, err := readCodexUsage(ctx, c.auth)
	if err != nil {
		return nil, err
	}
	u := codexUsageToUsage(c.Name(), body, r, r.RateLimit)
	if credits, err := readCodexResetCredits(ctx, c.auth); err == nil {
		u.ResetCredits = credits
	} else if r.ResetCredits != nil {
		// The detail endpoint is private and may go away; the usage response
		// itself now embeds the available count, so keep at least that.
		u.ResetCredits = &usage.ResetCredits{AvailableCount: r.ResetCredits.AvailableCount}
	}
	return u, nil
}

func (c *Codex) Trigger(ctx context.Context, dryRun bool) (*TriggerResult, error) {
	return triggerCodex(ctx, c.cfg, dryRun)
}

// RedeemResetCredit spends the next available reset credit right now. Each call
// is a distinct attempt, so it carries a fresh idempotency key.
func (c *Codex) RedeemResetCredit(ctx context.Context) (string, error) {
	return c.consumeResetCredit(ctx, randomIdempotencyKey())
}

// AutoRedeemResetCredit spends a credit that is about to lapse, at most once per
// codexRedeemCooldown. The key is derived from the credit itself, so an attempt
// whose response was lost in flight is retried — after the cooldown — under the
// same key and cannot spend a second credit.
func (c *Codex) AutoRedeemResetCredit(ctx context.Context, u *usage.Usage) (string, error) {
	credit, ok := u.ResetCreditToRedeem(time.Now())
	if !ok {
		return "", nil
	}
	c.redeemMu.Lock()
	if time.Since(c.lastRedeem) < codexRedeemCooldown {
		c.redeemMu.Unlock()
		return "", nil
	}
	c.lastRedeem = time.Now()
	c.redeemMu.Unlock()
	return c.consumeResetCredit(ctx, creditIdempotencyKey(credit))
}

// consumeResetCredit redeems one banked reset credit. The credit id is
// deliberately omitted: the backend then picks the next available credit — the
// same one the policy targets — so we don't depend on an id field this private
// endpoint doesn't document.
func (c *Codex) consumeResetCredit(ctx context.Context, idempotencyKey string) (string, error) {
	payload, err := json.Marshal(map[string]string{"idempotency_key": idempotencyKey})
	if err != nil {
		return "", err
	}
	accountID, _ := c.auth.AccountID(ctx)
	body, err := fetchWithAuth(ctx, c.auth, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexConsumeURL(), bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", codexUserAgent)
		req.Header.Set("OpenAI-Beta", "codex-1")
		req.Header.Set("originator", "Codex Desktop")
		if accountID != "" {
			req.Header.Set("ChatGPT-Account-Id", accountID)
		}
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("codex reset credit consume: %w", err)
	}
	var r struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("codex reset credit consume: parsing response: %w", err)
	}
	if r.Code == "" {
		return "", fmt.Errorf("codex reset credit consume: no outcome in response: %s", truncate(body, 200))
	}
	return normalizeRedeemOutcome(r.Code), nil
}

// normalizeRedeemOutcome folds the two spellings of the same outcomes into the
// snake_case form we report: the private endpoint answers in snake_case, while
// Codex's app-server protocol spells them in camelCase. Unknown codes pass
// through untouched rather than being reported as a success.
func normalizeRedeemOutcome(code string) string {
	switch code {
	case "nothingToReset":
		return RedeemNothingToReset
	case "noCredit":
		return RedeemNoCredit
	case "alreadyRedeemed":
		return RedeemAlreadyRedeemed
	default:
		return code
	}
}

func randomIdempotencyKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("limitping-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// creditIdempotencyKey derives a stable key from the credit being spent, so the
// same credit always maps to the same logical attempt.
func creditIdempotencyKey(c usage.ResetCredit) string {
	sum := sha256.Sum256([]byte("limitping-reset-credit|" + c.ExpiresAt.UTC().Format(time.RFC3339)))
	return hex.EncodeToString(sum[:16])
}

// Spark is a separate provider backed by Codex auth and CLI transport.
// Its usage window is the Spark-specific entry inside the Codex usage payload.
type Spark struct {
	cfg  config.ProviderConfig
	auth *auth.CodexAuth
}

// NewSpark returns the Spark provider. It shares Codex credentials and the
// Codex CLI binary, but it owns provider identity and usage selection.
func NewSpark(cfg config.ProviderConfig) *Spark {
	if cfg.Model == "" {
		cfg.Model = sparkDefaultModel
	}
	return &Spark{
		cfg:  cfg,
		auth: auth.NewCodexAuth(),
	}
}

func (s *Spark) Name() string { return "spark" }

func (s *Spark) ActiveTask(ctx context.Context) (string, bool, error) {
	return codexActiveTask(ctx)
}

func (s *Spark) ReadUsage(ctx context.Context) (*usage.Usage, error) {
	body, r, err := readCodexUsage(ctx, s.auth)
	if err != nil {
		return nil, err
	}
	rateLimit, err := sparkRateLimitFromResponse(r, s.cfg.Model)
	if err != nil {
		return nil, err
	}
	return codexUsageToUsage(s.Name(), body, r, rateLimit), nil
}

func (s *Spark) Trigger(ctx context.Context, dryRun bool) (*TriggerResult, error) {
	return triggerCodex(ctx, s.cfg, dryRun)
}

func codexActiveTask(_ context.Context) (string, bool, error) {
	// Active-session detection relies entirely on the Codex CLI hooks (see
	// `limitping hooks install`). Without them we don't guess from the process
	// list; the scheduler just pings. Spark uses the same activity signal
	// because its actual CLI session is still `codex`.
	if !activity.Enabled("codex") {
		return "", false, nil
	}
	return activity.Active("codex")
}

type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int     `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

// The windows are pointers because the backend nulls out a window when that
// limit is not currently enforced: since OpenAI temporarily removed the 5h
// limit on 2026-07-12, primary_window carries the weekly window and
// secondary_window is null.
type codexRateLimit struct {
	Allowed      bool         `json:"allowed"`
	LimitReached bool         `json:"limit_reached"`
	Primary      *codexWindow `json:"primary_window"`
	Secondary    *codexWindow `json:"secondary_window"`
}

type codexAdditionalRateLimit struct {
	LimitName      string         `json:"limit_name"`
	MeteredFeature string         `json:"metered_feature"`
	RateLimit      codexRateLimit `json:"rate_limit"`
}

type codexCredits struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

type codexUsageResp struct {
	PlanType             string                     `json:"plan_type"`
	RateLimit            codexRateLimit             `json:"rate_limit"`
	AdditionalRateLimits []codexAdditionalRateLimit `json:"additional_rate_limits"`
	Credits              *codexCredits              `json:"credits"`
	ResetCredits         *codexInlineResetCredits   `json:"rate_limit_reset_credits"`
}

// codexInlineResetCredits is the reset-credit count embedded in the usage
// response itself; a fallback when the detail endpoint is unavailable.
type codexInlineResetCredits struct {
	AvailableCount int `json:"available_count"`
}

type codexResetCreditsResp struct {
	AvailableCount *int               `json:"available_count"`
	Credits        []codexResetCredit `json:"credits"`
}

type codexResetCredit struct {
	Status     string `json:"status"`
	GrantedAt  string `json:"granted_at"`
	ExpiresAt  string `json:"expires_at"`
	RedeemedAt string `json:"redeemed_at"`
}

func readCodexUsage(ctx context.Context, auth *auth.CodexAuth) ([]byte, codexUsageResp, error) {
	var r codexUsageResp
	accountID, _ := auth.AccountID(ctx)
	body, err := fetchWithAuth(ctx, auth, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", codexUserAgent)
		if accountID != "" {
			req.Header.Set("ChatGPT-Account-Id", accountID)
		}
		return req, nil
	})
	if err != nil {
		return nil, r, err
	}

	if err := json.Unmarshal(body, &r); err != nil {
		return nil, r, fmt.Errorf("codex usage: parsing response: %w", err)
	}
	return body, r, nil
}

func readCodexResetCredits(ctx context.Context, auth *auth.CodexAuth) (*usage.ResetCredits, error) {
	var r codexResetCreditsResp
	accountID, _ := auth.AccountID(ctx)
	body, err := fetchWithAuth(ctx, auth, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexResetCreditsURL(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", codexUserAgent)
		req.Header.Set("OpenAI-Beta", "codex-1")
		req.Header.Set("originator", "Codex Desktop")
		if accountID != "" {
			req.Header.Set("ChatGPT-Account-Id", accountID)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("codex reset credits: parsing response: %w", err)
	}
	return codexResetCreditsToUsage(r), nil
}

func codexUsageToUsage(provider string, body []byte, r codexUsageResp, rateLimit codexRateLimit) *usage.Usage {
	fiveHour, weekly := codexWindowsFromRateLimit(rateLimit)
	u := &usage.Usage{
		Provider:     provider,
		Plan:         r.PlanType,
		FetchedAt:    time.Now(),
		Raw:          body,
		LimitReached: rateLimit.LimitReached,
		FiveHour:     fiveHour,
		Weekly:       weekly,
	}
	if r.Credits != nil {
		u.Credits = &usage.Credits{
			HasCredits: r.Credits.HasCredits,
			Unlimited:  r.Credits.Unlimited,
			Balance:    r.Credits.Balance,
		}
	}
	return u
}

func codexResetCreditsToUsage(r codexResetCreditsResp) *usage.ResetCredits {
	credits := make([]usage.ResetCredit, 0, len(r.Credits))
	for _, c := range r.Credits {
		credits = append(credits, usage.ResetCredit{
			Status:     c.Status,
			GrantedAt:  parseCodexResetTime(c.GrantedAt),
			ExpiresAt:  parseCodexResetTime(c.ExpiresAt),
			RedeemedAt: parseCodexResetTime(c.RedeemedAt),
		})
	}
	count := len(credits)
	if r.AvailableCount != nil {
		count = *r.AvailableCount
	}
	return &usage.ResetCredits{
		AvailableCount: count,
		Credits:        credits,
	}
}

func parseCodexResetTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func sparkRateLimitFromResponse(r codexUsageResp, model string) (codexRateLimit, error) {
	target := normalizeCodexLimitName(model)
	for _, additional := range r.AdditionalRateLimits {
		if normalizeCodexLimitName(additional.LimitName) == target {
			return additional.RateLimit, nil
		}
	}
	return codexRateLimit{}, fmt.Errorf("codex usage: no rate limit found for provider %q model %q", "spark", model)
}

func normalizeCodexLimitName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func codexUsageURL() string {
	base := codexDefaultBaseURL
	if contents, err := os.ReadFile(codexConfigPath()); err == nil {
		if configured := parseCodexBaseURL(string(contents)); configured != "" {
			base = configured
		}
	}
	return codexUsageURLFromBase(base)
}

func codexResetCreditsURL() string {
	base := codexDefaultBaseURL
	if contents, err := os.ReadFile(codexConfigPath()); err == nil {
		if configured := parseCodexBaseURL(string(contents)); configured != "" {
			base = configured
		}
	}
	return codexResetCreditsURLFromBase(base)
}

func codexUsageURLFromBase(base string) string {
	normalized := normalizeCodexBaseURL(base)
	path := codexAPIPath
	if strings.Contains(normalized, "/backend-api") {
		path = codexChatGPTPath
	}
	endpoint := normalized + path
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return codexDefaultBaseURL + codexChatGPTPath
	}
	return endpoint
}

func codexResetCreditsURLFromBase(base string) string {
	return codexResetURLFromBase(base, codexResetPath)
}

func codexConsumeURL() string {
	base := codexDefaultBaseURL
	if contents, err := os.ReadFile(codexConfigPath()); err == nil {
		if configured := parseCodexBaseURL(string(contents)); configured != "" {
			base = configured
		}
	}
	return codexResetURLFromBase(base, codexConsumePath)
}

// codexResetURLFromBase builds a reset-credit endpoint. These live only on the
// ChatGPT backend, so a base pointing elsewhere falls back to the default.
func codexResetURLFromBase(base, path string) string {
	normalized := normalizeCodexBaseURL(base)
	if !strings.Contains(normalized, "/backend-api") {
		normalized = codexDefaultBaseURL
	}
	endpoint := normalized + path
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return codexDefaultBaseURL + path
	}
	return endpoint
}

func normalizeCodexBaseURL(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = codexDefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	if (strings.HasPrefix(base, "https://chatgpt.com") || strings.HasPrefix(base, "https://chat.openai.com")) &&
		!strings.Contains(base, "/backend-api") {
		base += "/backend-api"
	}
	return base
}

func parseCodexBaseURL(contents string) string {
	var cfg struct {
		ChatGPTBaseURL string `toml:"chatgpt_base_url"`
	}
	if _, err := toml.Decode(contents, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.ChatGPTBaseURL)
}

func codexConfigPath() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return filepath.Join(h, "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".codex", "config.toml")
	}
	return filepath.Join(home, ".codex", "config.toml")
}

// codexWindowsFromRateLimit classifies the windows by length rather than
// position. Historically primary was the 5h window and secondary the weekly
// one, but with the 5h limit removed the weekly window is the (only) primary,
// so position no longer identifies a window. A window a couple of days or
// longer is the weekly one; anything shorter is the 5h one. A limit whose
// window is absent stays the zero Window (usage.Window.Missing).
func codexWindowsFromRateLimit(rl codexRateLimit) (fiveHour, weekly usage.Window) {
	const weeklyMinSeconds = 2 * 24 * 60 * 60
	for _, w := range []*codexWindow{rl.Primary, rl.Secondary} {
		if w == nil {
			continue
		}
		if w.LimitWindowSeconds >= weeklyMinSeconds {
			weekly = codexWindowToUsage(*w)
		} else {
			fiveHour = codexWindowToUsage(*w)
		}
	}
	return fiveHour, weekly
}

func codexWindowToUsage(w codexWindow) usage.Window {
	var resetsAt time.Time
	if w.ResetAt > 0 {
		resetsAt = time.Unix(w.ResetAt, 0)
	}
	return usage.Window{
		UsedPercent:   w.UsedPercent,
		ResetsAt:      resetsAt,
		WindowSeconds: w.LimitWindowSeconds,
	}
}

func triggerCodex(ctx context.Context, cfg config.ProviderConfig, dryRun bool) (*TriggerResult, error) {
	return triggerCodexWithTiming(ctx, cfg, dryRun, defaultCodexInteractiveTiming)
}

func triggerCodexWithTiming(ctx context.Context, cfg config.ProviderConfig, dryRun bool, timing codexInteractiveTiming) (*TriggerResult, error) {
	prompt := cfg.Prompt
	if prompt == "" {
		prompt = "ok"
	}
	args := []string{}
	if cfg.ReasoningEffort != "" {
		args = append(args, "-c", "model_reasoning_effort="+cfg.ReasoningEffort)
	}
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}
	args = append(args, codexInteractiveArgs(cfg.ExtraArgs)...)
	// A PTY is always treated as focused, so force Codex's turn-complete OSC 9
	// notification and use it as the exact boundary before stopping the TUI.
	args = append(args,
		"-c", `tui.notifications=["agent-turn-complete"]`,
		"-c", `tui.notification_method="osc9"`,
		"-c", `tui.notification_condition="always"`,
	)
	args = append(args, prompt)
	res := &TriggerResult{Command: "codex " + shellJoin(args)}
	if dryRun {
		return res, nil
	}

	cmd := exec.CommandContext(ctx, "codex", args...)
	if term := os.Getenv("TERM"); term == "" || term == "dumb" {
		cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return res, fmt.Errorf("codex interactive failed to start: %w", err)
	}
	defer ptmx.Close()

	output := &limitedBuffer{limit: 4096}
	markers := newCodexTurnMarkers()
	go func() {
		_, _ = io.Copy(io.MultiWriter(output, markers), ptmx)
	}()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	if terminal, err := codexAwait(ctx, cmd, ptmx, output, markers.completed, done, timing.maxWait); terminal {
		return res, err
	}

	return res, codexInteractiveStop(ctx, cmd, ptmx, done, output, timing.exitGrace)
}

type codexTurnMarkers struct {
	buf       []byte
	finished  bool
	completed chan struct{}
}

func newCodexTurnMarkers() *codexTurnMarkers {
	return &codexTurnMarkers{completed: make(chan struct{})}
}

func (m *codexTurnMarkers) Write(p []byte) (int, error) {
	if m.finished {
		return len(p), nil
	}
	m.buf = append(m.buf, p...)
	if start := bytes.Index(m.buf, []byte(codexTurnComplete)); start >= 0 && bytes.IndexByte(m.buf[start:], 0x07) >= 0 {
		close(m.completed)
		m.finished = true
		m.buf = nil
	}
	if len(m.buf) > codexTurnScanLimit {
		m.buf = append(m.buf[:0], m.buf[len(m.buf)-codexTurnScanLimit:]...)
	}
	return len(p), nil
}

func codexAwait(ctx context.Context, cmd *exec.Cmd, ptmx *os.File, output *limitedBuffer, completed <-chan struct{}, done <-chan error, maxWait time.Duration) (bool, error) {
	select {
	case <-completed:
		return false, nil
	case err := <-done:
		return true, codexInteractiveErr(err, output)
	case <-ctx.Done():
		return true, codexInteractiveCancel(ctx, cmd, ptmx, done, output)
	case <-time.After(maxWait):
		return false, nil
	}
}

func codexInteractiveStop(ctx context.Context, cmd *exec.Cmd, ptmx *os.File, done <-chan error, output *limitedBuffer, exitGrace time.Duration) error {
	deadline := time.After(exitGrace)
	ticker := time.NewTicker(exitGrace / 2)
	defer ticker.Stop()

	for sent := false; ; {
		if !sent {
			_, _ = ptmx.Write([]byte{0x03})
			sent = true
		}
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return codexInteractiveCancel(ctx, cmd, ptmx, done, output)
		case <-ticker.C:
			_, _ = ptmx.Write([]byte{0x03})
		case <-deadline:
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return nil
		}
	}
}

func codexInteractiveErr(err error, output *limitedBuffer) error {
	if err == nil {
		return nil
	}
	tail := truncate(output.Bytes(), 300)
	if tail == "" {
		return fmt.Errorf("codex interactive failed: %w", err)
	}
	return fmt.Errorf("codex interactive failed: %w: %s", err, tail)
}

func codexInteractiveCancel(ctx context.Context, cmd *exec.Cmd, ptmx *os.File, done <-chan error, output *limitedBuffer) error {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = ptmx.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	tail := truncate(output.Bytes(), 300)
	if tail == "" {
		return fmt.Errorf("codex interactive cancelled: %w", ctx.Err())
	}
	return fmt.Errorf("codex interactive cancelled: %w: %s", ctx.Err(), tail)
}

func codexInteractiveArgs(extra []string) []string {
	out := make([]string, 0, len(extra))
	for i := 0; i < len(extra); i++ {
		arg := extra[i]
		flag, inlineValue := splitFlagValue(arg)
		if codexInteractiveUnsupportedValueArg(flag) {
			if !inlineValue && i+1 < len(extra) {
				i++
			}
			continue
		}
		if codexInteractiveUnsupportedArg(flag) {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func codexInteractiveUnsupportedArg(flag string) bool {
	switch flag {
	case "--skip-git-repo-check", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--json":
		return true
	default:
		return false
	}
}

func codexInteractiveUnsupportedValueArg(flag string) bool {
	switch flag {
	case "--output-schema", "--output-last-message", "--color", "-o":
		return true
	default:
		return false
	}
}
