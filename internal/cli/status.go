package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/spend"
	"github.com/wavever/CCLimitPing/internal/usage"
)

// spendTimeout caps the local transcript scan. It runs alongside the usage
// fetch, so it normally costs nothing in wall time; the cap is there so a huge
// or unreadable transcript history cannot hold up the whole command.
const spendTimeout = 20 * time.Second

func newStatusCmd() *cobra.Command {
	var verbose bool
	var jsonOut bool
	text := localizedText()
	cmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"s", "stat"},
		Short:   text.statusShort,
		Long:    text.statusLong,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !jsonOut {
				updateNotice(cmd.Context(), cmd.OutOrStdout(), text, os.Stdin)
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			providers := enabledProviders(cfg)
			if len(providers) == 0 {
				return fmt.Errorf("no providers enabled in config")
			}
			return runStatus(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), text, providers, verbose, jsonOut, cfg.UsageDisplay, true)
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, text.statusVerboseFlag)
	cmd.Flags().BoolVar(&jsonOut, "json", false, text.statusJSONFlag)
	return cmd
}

func runStatus(ctx context.Context, out, progress io.Writer, text cliText, providers []provider.Provider, verbose, jsonOut bool, display string, withResetCredits bool) error {
	if progress == nil {
		progress = io.Discard
	}
	display = normalizeUsageDisplay(display)
	// In JSON mode keep stdout a single valid document: suppress the
	// "Fetching..." progress chatter that would otherwise interleave.
	if jsonOut {
		progress = io.Discard
	}
	failed := 0
	entries := make([]statusJSON, 0, len(providers))
	for _, p := range providers {
		// Started first and collected last: reading the day's transcripts is
		// pure local I/O, so it rides along with the network round trip instead
		// of adding to it.
		spendCh := make(chan *spend.Day, 1)
		go func() { spendCh <- todaySpend(ctx, p.Name()) }()

		if text.statusFetchingFmt != "" {
			fmt.Fprintf(progress, text.statusFetchingFmt, p.Name())
		}
		read := p.ReadUsage
		if withResetCredits {
			read = func(ctx context.Context) (*usage.Usage, error) { return readUsageWithResetCredits(ctx, p) }
		}
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		u, err := read(readCtx)
		cancel()
		day := <-spendCh
		if err != nil {
			failed++
			if jsonOut {
				entries = append(entries, statusJSON{Provider: p.Name(), Error: err.Error()})
				continue
			}
			fmt.Fprintf(out, text.statusErrorFmt, p.Name(), localizedProviderError(text, err))
			continue
		}
		if jsonOut {
			entries = append(entries, newStatusJSON(u, verbose, day))
			continue
		}
		printUsage(out, text, u, verbose, display, day)
	}
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entries); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("status failed for %d provider(s)", failed)
	}
	return nil
}

func localizedProviderError(text cliText, err error) string {
	var accessErr *provider.ClaudeSubscriptionAccessError
	if errors.As(err, &accessErr) && text.statusSubAccessError != "" {
		return text.statusSubAccessError
	}
	return err.Error()
}

// statusJSON is the stable, documented shape emitted by `status --json`. It is
// decoupled from usage.Usage so the internal model can evolve without breaking
// scripts that consume this output.
type statusJSON struct {
	Provider     string            `json:"provider"`
	Plan         string            `json:"plan,omitempty"`
	FiveHour     *windowJSON       `json:"five_hour,omitempty"`
	Weekly       *windowJSON       `json:"weekly,omitempty"`
	Scoped       []scopedJSON      `json:"scoped_limits,omitempty"`
	Credits      *creditsJSON      `json:"credits,omitempty"`
	ResetCredits *resetCreditsJSON `json:"reset_credits,omitempty"`
	// Why the reset credits could not be read, when they could not.
	ResetCreditsError string          `json:"reset_credits_error,omitempty"`
	Today             *todayJSON      `json:"today,omitempty"`
	LimitReached      bool            `json:"limit_reached"`
	FetchedAt         string          `json:"fetched_at,omitempty"`
	Raw               json.RawMessage `json:"raw,omitempty"`
	Error             string          `json:"error,omitempty"`
}

// scopedJSON is a limit narrower than the plan-wide windows, e.g. one model's
// weekly cap.
type scopedJSON struct {
	Label string `json:"label"`
	*windowJSON
}

type windowJSON struct {
	UsedPercent      float64 `json:"used_percent"`
	RemainingPercent float64 `json:"remaining_percent"`
	Active           bool    `json:"active"`
	ResetsAt         string  `json:"resets_at,omitempty"`
	RemainingSeconds int     `json:"remaining_seconds"`
	WindowSeconds    int     `json:"window_seconds,omitempty"`
}

type creditsJSON struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance,omitempty"`
}

type resetCreditsJSON struct {
	AvailableCount int               `json:"available_count"`
	Credits        []resetCreditJSON `json:"credits,omitempty"`
	// Why the provider offers this caller no reset cards (Claude's
	// ineligible_reason), when that is worth knowing.
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	// When the backend next accepts a reset, if it has said.
	CooldownUntil string `json:"cooldown_until,omitempty"`
}

type resetCreditJSON struct {
	Status     string `json:"status,omitempty"`
	GrantedAt  string `json:"granted_at,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	RedeemedAt string `json:"redeemed_at,omitempty"`
	// Claude reset cards only.
	ID            string   `json:"id,omitempty"`
	Label         string   `json:"label,omitempty"`
	Left          int      `json:"left,omitempty"`
	Total         int      `json:"total,omitempty"`
	Clears        []string `json:"clears,omitempty"`
	RequiresLimit bool     `json:"requires_limit,omitempty"`
}

// todayJSON is the local day's token consumption, read from the provider CLI's
// own transcripts. cost_usd is what those tokens would cost at API rates;
// cost_complete is false when a model that ran had no published rates, which
// makes cost_usd a lower bound.
type todayJSON struct {
	Date                string           `json:"date"`
	InputTokens         int              `json:"input_tokens"`
	CacheReadTokens     int              `json:"cache_read_tokens"`
	CacheCreationTokens int              `json:"cache_creation_tokens"`
	OutputTokens        int              `json:"output_tokens"`
	TotalTokens         int              `json:"total_tokens"`
	CostUSD             float64          `json:"cost_usd"`
	CostComplete        bool             `json:"cost_complete"`
	Models              []todayModelJSON `json:"models,omitempty"`
}

type todayModelJSON struct {
	Model       string  `json:"model,omitempty"`
	TotalTokens int     `json:"total_tokens"`
	CostUSD     float64 `json:"cost_usd"`
}

func newStatusJSON(u *usage.Usage, verbose bool, day *spend.Day) statusJSON {
	s := statusJSON{
		Provider:     u.Provider,
		Plan:         u.Plan,
		LimitReached: u.LimitReached,
	}
	// A missing window means the provider does not currently enforce that
	// limit; drop the key rather than emit an all-zero window.
	if !u.FiveHour.Missing() {
		s.FiveHour = newWindowJSON(u.FiveHour)
	}
	if !u.Weekly.Missing() {
		s.Weekly = newWindowJSON(u.Weekly)
	}
	for _, l := range u.ScopedLimits {
		s.Scoped = append(s.Scoped, scopedJSON{Label: l.Label, windowJSON: newWindowJSON(l.Window)})
	}
	if !u.FetchedAt.IsZero() {
		s.FetchedAt = u.FetchedAt.Format(time.RFC3339)
	}
	if u.Credits != nil {
		s.Credits = &creditsJSON{
			HasCredits: u.Credits.HasCredits,
			Unlimited:  u.Credits.Unlimited,
			Balance:    u.Credits.Balance,
		}
	}
	if u.ResetCredits != nil {
		s.ResetCredits = newResetCreditsJSON(u.ResetCredits)
	}
	if u.ResetCreditsError != nil {
		s.ResetCreditsError = u.ResetCreditsError.Error()
	}
	s.Today = newTodayJSON(day)
	if verbose && json.Valid(u.Raw) {
		s.Raw = json.RawMessage(u.Raw)
	}
	return s
}

func newWindowJSON(w usage.Window) *windowJSON {
	j := &windowJSON{
		UsedPercent:      w.UsedPercent,
		RemainingPercent: remainingPercent(w.UsedPercent),
		Active:           w.Active(),
		RemainingSeconds: int(w.Remaining().Seconds()),
		WindowSeconds:    w.WindowSeconds,
	}
	if !w.ResetsAt.IsZero() {
		j.ResetsAt = w.ResetsAt.Format(time.RFC3339)
	}
	return j
}

func newResetCreditsJSON(rc *usage.ResetCredits) *resetCreditsJSON {
	out := &resetCreditsJSON{
		AvailableCount:    rc.AvailableCount,
		Credits:           make([]resetCreditJSON, 0, len(rc.Credits)),
		UnavailableReason: rc.UnavailableReason,
		CooldownUntil:     timeJSON(rc.CooldownUntil),
	}
	for _, c := range rc.Credits {
		out.Credits = append(out.Credits, resetCreditJSON{
			Status:        c.Status,
			GrantedAt:     timeJSON(c.GrantedAt),
			ExpiresAt:     timeJSON(c.ExpiresAt),
			RedeemedAt:    timeJSON(c.RedeemedAt),
			ID:            c.ID,
			Label:         c.Label,
			Left:          c.Left,
			Total:         c.Total,
			Clears:        c.Clears,
			RequiresLimit: c.RequiresLimit,
		})
	}
	return out
}

// newTodayJSON renders the day's spend, or nothing at all when the provider's
// CLI has never run on this machine — an absent key says "no local data", which
// zeros would misreport as "nothing was spent".
func newTodayJSON(day *spend.Day) *todayJSON {
	if day == nil || !day.Available {
		return nil
	}
	out := &todayJSON{
		Date:                day.Date.Format("2006-01-02"),
		InputTokens:         day.Tokens.Input,
		CacheReadTokens:     day.Tokens.CacheRead,
		CacheCreationTokens: day.Tokens.CacheWrite,
		OutputTokens:        day.Tokens.Output,
		TotalTokens:         day.Tokens.Total(),
		CostUSD:             roundUSD(day.CostUSD),
		CostComplete:        day.Priced,
	}
	for _, m := range day.Models {
		out.Models = append(out.Models, todayModelJSON{
			Model:       m.Model,
			TotalTokens: m.Tokens.Total(),
			CostUSD:     roundUSD(m.CostUSD),
		})
	}
	return out
}

// roundUSD trims the float noise (0.30000000000000004) that summing per-model
// costs leaves behind, at a precision finer than any real per-day total needs.
func roundUSD(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

func timeJSON(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func printUsage(out io.Writer, text cliText, u *usage.Usage, verbose bool, display string, day *spend.Day) {
	display = normalizeUsageDisplay(display)
	plan := u.Plan
	if plan != "" {
		plan = " (" + plan + ")"
	}
	fmt.Fprintf(out, "%s%s\n", u.Provider, plan)
	fmt.Fprintf(out, text.statusFiveHourLineFmt, fmtWindow(text, u.FiveHour, display))
	fmt.Fprintf(out, text.statusWeeklyLineFmt, fmtWindow(text, u.Weekly, display))
	for _, l := range u.ScopedLimits {
		fmt.Fprintf(out, text.statusScopedLineFmt, l.Label, fmtWindow(text, l.Window, display))
	}
	printToday(out, text, day, verbose)
	if u.Credits != nil && (u.Credits.HasCredits || u.Credits.Unlimited) {
		if u.Credits.Unlimited {
			fmt.Fprint(out, text.statusCreditsUnlimited)
		} else {
			fmt.Fprintf(out, text.statusCreditsFmt, u.Credits.Balance)
		}
	}
	printResetCredits(out, text, u.ResetCredits)
	// A card read that failed is not the same as holding none, but saying so
	// on every status would be noise for the many accounts that never hold any.
	if verbose && u.ResetCreditsError != nil {
		fmt.Fprintf(out, text.statusResetCreditsUnreadFmt, u.ResetCreditsError)
	}
	if verbose {
		fmt.Fprintf(out, "  raw: %s\n", string(u.Raw))
	}
	fmt.Fprintln(out)
}

// todaySpend reads what the provider's local CLI sessions consumed today. It is
// a best-effort extra: a transcript that cannot be read costs the line, never
// the status command.
func todaySpend(ctx context.Context, name string) *spend.Day {
	ctx, cancel := context.WithTimeout(ctx, spendTimeout)
	defer cancel()
	day, err := spend.Today(ctx, name)
	if err != nil && day.Empty() {
		return nil
	}
	return &day
}

// printToday renders the day's token consumption and what it would have cost at
// API rates — the usage endpoints report percentages only, so this is the one
// place a subscription's actual consumption becomes a number. Nothing is
// printed for a provider whose CLI has never run on this machine: silence is
// honest there, while "0 tok" would claim a quiet day.
func printToday(out io.Writer, text cliText, day *spend.Day, verbose bool) {
	if day == nil || !day.Available {
		return
	}
	fmt.Fprintf(out, text.statusTodayLineFmt, fmtSpend(text, day.Tokens.Total(), day.CostUSD))
	if !verbose || day.Empty() {
		return
	}
	fmt.Fprintf(out, text.statusTodayBreakdownFmt,
		humanTokens(day.Tokens.Input), humanTokens(day.Tokens.CacheRead),
		humanTokens(day.Tokens.CacheWrite), humanTokens(day.Tokens.Output))
	for _, m := range day.Models {
		name := m.Model
		if name == "" {
			name = text.statusTodayUnknownModel
		}
		fmt.Fprintf(out, text.statusTodayModelFmt, name, fmtSpend(text, m.Tokens.Total(), m.CostUSD))
	}
}

// fmtSpend renders "47.9M tok  ≈ $38.15", dropping the cost when the model's
// rates are unknown (an unpublished or brand-new model).
func fmtSpend(text cliText, tokens int, costUSD float64) string {
	s := fmt.Sprintf(text.statusTodayTokensFmt, humanTokens(tokens))
	if costUSD > 0 {
		s += fmt.Sprintf(text.statusTodayCostFmt, fmtUSD(costUSD))
	}
	return s
}

// humanTokens keeps the status line scannable: exact below 10k, where the digits
// are still readable, and abbreviated above it. `--json` carries exact counts.
func humanTokens(n int) string {
	switch {
	case n < 10_000:
		return humanInt(n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	case n < 1_000_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	default:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	}
}

// fmtUSD shows cents, falling back to four decimals for the sums too small to
// register in them.
func fmtUSD(v float64) string {
	if v < 0.01 {
		return fmt.Sprintf("%.4f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

func printResetCredits(out io.Writer, text cliText, rc *usage.ResetCredits) {
	if rc != nil && rc.UnavailableReason != "" {
		fmt.Fprintf(out, text.statusResetCreditsUnavailableFmt, resetCardsUnavailableText(text, rc.UnavailableReason))
		return
	}
	if rc == nil || (rc.AvailableCount == 0 && len(rc.Credits) == 0) {
		return
	}
	countFmt := text.statusResetCreditsManyFmt
	if rc.AvailableCount == 1 {
		countFmt = text.statusResetCreditsOneFmt
	}
	fmt.Fprintf(out, countFmt, rc.AvailableCount)
	for _, c := range rc.Credits {
		line := resetCreditLine(text, c)
		if line != "" {
			fmt.Fprintf(out, "    - %s\n", line)
		}
	}
}

func resetCreditLine(text cliText, c usage.ResetCredit) string {
	status := c.Status
	if status == "" {
		switch {
		case !c.RedeemedAt.IsZero():
			status = "redeemed"
		case !c.ExpiresAt.IsZero() && time.Now().After(c.ExpiresAt):
			status = "expired"
		default:
			status = "available"
		}
	}
	parts := []string{creditStatusWord(text, status)}
	if c.Label != "" {
		parts = append(parts, c.Label)
	}
	// A multi-use grant; a single reset needs no count.
	if c.Total > 1 {
		parts = append(parts, fmt.Sprintf(text.statusCreditLeftFmt, c.Left, c.Total))
	}
	if windows := clearedWindowNames(text, c.Clears); windows != "" {
		parts = append(parts, fmt.Sprintf(text.statusCreditClearsFmt, windows))
	}
	if c.RequiresLimit {
		parts = append(parts, text.statusCreditAtLimitOnly)
	}
	if !c.GrantedAt.IsZero() {
		parts = append(parts, fmt.Sprintf(text.statusCreditGrantedFmt, c.GrantedAt.Local().Format(text.statusCreditTimeLayout)))
	}
	if !c.ExpiresAt.IsZero() {
		// The zone is stated on the expiry — the one date on this line that is a
		// deadline to act on — and carries the line's other stamps with it.
		expires := c.ExpiresAt.Local()
		part := fmt.Sprintf(text.statusCreditExpiresFmt, expires.Format(text.statusCreditTimeLayout)+" "+fmtZone(expires))
		// Remaining lifetime, so an unredeemed credit about to lapse is
		// visible at a glance. Meaningless once redeemed or expired.
		if remaining := time.Until(c.ExpiresAt); remaining > 0 && c.RedeemedAt.IsZero() {
			part += fmt.Sprintf(text.statusCreditExpiresInFmt, fmtDurDays(text, remaining))
		}
		parts = append(parts, part)
	}
	if !c.RedeemedAt.IsZero() {
		parts = append(parts, fmt.Sprintf(text.statusCreditRedeemedFmt, c.RedeemedAt.Local().Format(text.statusCreditTimeLayout)))
	}
	return strings.Join(parts, text.statusListSep)
}

// creditStatusWord localizes the well-known reset-credit statuses; anything
// else (a new API value) passes through untranslated.
func creditStatusWord(text cliText, status string) string {
	switch status {
	case "available":
		return text.statusCreditAvailable
	case "redeemed":
		return text.statusCreditRedeemed
	case "expired":
		return text.statusCreditExpired
	case provider.ClaudeGrantQueued:
		return text.statusCreditQueued
	case provider.ClaudeGrantPaused:
		return text.statusCreditPaused
	case provider.ClaudeGrantPending:
		return text.statusCreditPending
	default:
		return status
	}
}

// clearedWindowNames renders the windows a reset restores ("5h + weekly").
// Limits limitping has no name for (Claude's per-model weekly buckets) are
// shown as the provider names them.
func clearedWindowNames(text cliText, clears []string) string {
	names := make([]string, 0, len(clears))
	for _, w := range clears {
		switch w {
		case usage.ClearsFiveHour:
			names = append(names, text.statusClearsFiveHour)
		case usage.ClearsWeekly:
			names = append(names, text.statusClearsWeekly)
		default:
			names = append(names, w)
		}
	}
	return strings.Join(names, " + ")
}

func fmtWindow(text cliText, w usage.Window, display string) string {
	if w.Missing() {
		return text.statusNotEnforced
	}
	display = normalizeUsageDisplay(display)
	pct := displayedPercent(w, display)
	bar := usageBar(pct)
	word := text.statusUsedWord
	if display == "remaining" {
		word = text.statusRemainingWord
	}
	if w.ResetsAt.IsZero() {
		return fmt.Sprintf(text.statusWindowNoResetFmt, bar, pct, word)
	}
	return fmt.Sprintf(text.statusWindowFmt,
		bar, pct, word, fmtDur(text, w.Remaining()), fmtClock(text, w.ResetsAt))
}

// fmtClock renders the reset wall-clock time with a localized weekday name and
// the zone it is expressed in.
func fmtClock(text cliText, t time.Time) string {
	lt := t.Local()
	clock := lt.Format("Mon 15:04")
	if text.statusWeekdays != ([7]string{}) {
		clock = text.statusWeekdays[int(lt.Weekday())] + " " + lt.Format("15:04")
	}
	return clock + " " + fmtZone(lt)
}

// fmtZone renders t's UTC offset (UTC+8, UTC-5:30, UTC). The offset is used
// rather than the zone abbreviation because abbreviations are ambiguous — CST
// is both China Standard Time and US Central Standard Time.
func fmtZone(t time.Time) string {
	_, offset := t.Zone()
	if offset == 0 {
		return "UTC"
	}
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	if minutes := offset % 3600 / 60; minutes != 0 {
		return fmt.Sprintf("UTC%s%d:%02d", sign, offset/3600, minutes)
	}
	return fmt.Sprintf("UTC%s%d", sign, offset/3600)
}

func normalizeUsageDisplay(display string) string {
	if display == "remaining" {
		return "remaining"
	}
	return "used"
}

func displayedPercent(w usage.Window, display string) float64 {
	if normalizeUsageDisplay(display) == "remaining" {
		return remainingPercent(w.UsedPercent)
	}
	return w.UsedPercent
}

func remainingPercent(used float64) float64 {
	remaining := 100 - used
	if remaining < 0 {
		return 0
	}
	if remaining > 100 {
		return 100
	}
	return remaining
}

func usageBar(pct float64) string {
	const width = 10
	filled := int(pct/100*width + 0.5)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	b := make([]rune, width)
	for i := range b {
		if i < filled {
			b[i] = '█'
		} else {
			b[i] = '░'
		}
	}
	return "[" + string(b) + "]"
}

// fmtDurDays renders long spans with a day component (e.g. 11d16h) — reset
// credits live for 30 days, where pure hours would be unreadable — and falls
// back to fmtDur below one day.
func fmtDurDays(text cliText, d time.Duration) string {
	if d < 24*time.Hour {
		return fmtDur(text, d)
	}
	days := int(d / (24 * time.Hour))
	hours := int(d % (24 * time.Hour) / time.Hour)
	if hours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd%dh", days, hours)
}

func fmtDur(text cliText, d time.Duration) string {
	if d <= 0 {
		return text.statusNowWord
	}
	d = d.Round(time.Minute)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
