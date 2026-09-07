package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

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
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			providers := enabledProviders(cfg)
			if len(providers) == 0 {
				return fmt.Errorf("no providers enabled in config")
			}
			return runStatus(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), text, providers, verbose, jsonOut, cfg.UsageDisplay)
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, text.statusVerboseFlag)
	cmd.Flags().BoolVar(&jsonOut, "json", false, text.statusJSONFlag)
	return cmd
}

func runStatus(ctx context.Context, out, progress io.Writer, text cliText, providers []provider.Provider, verbose, jsonOut bool, display string) error {
	if progress == nil {
		progress = io.Discard
	}
	display = normalizeUsageDisplay(display)
	diagnostics := progress
	// In JSON mode keep stdout a single valid document: suppress the
	// "Fetching..." progress chatter that would otherwise interleave.
	if jsonOut {
		progress = io.Discard
	}
	failed := 0
	entries := make([]statusJSON, 0, len(providers))
	for _, p := range providers {
		if text.statusFetchingFmt != "" {
			fmt.Fprintf(progress, text.statusFetchingFmt, p.Name())
		}
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		u, err := p.ReadUsage(readCtx)
		cancel()
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
			if u.Verification != nil && u.Verification.Warning != "" {
				fmt.Fprintln(diagnostics, u.Verification.Warning)
			}
			entries = append(entries, newStatusJSON(u, verbose))
			continue
		}
		printUsage(out, text, u, verbose, display)
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
	Credits      *creditsJSON      `json:"credits,omitempty"`
	ResetCredits *resetCreditsJSON `json:"reset_credits,omitempty"`
	LimitReached bool              `json:"limit_reached"`
	FetchedAt    string            `json:"fetched_at,omitempty"`
	Raw          json.RawMessage   `json:"raw,omitempty"`
	Error        string            `json:"error,omitempty"`
}

type windowJSON struct {
	UsedPercent       float64 `json:"used_percent"`
	RemainingPercent  float64 `json:"remaining_percent"`
	Active            bool    `json:"active"`
	ResetsAt          string  `json:"resets_at,omitempty"`
	RemainingSeconds  int     `json:"remaining_seconds"`
	WindowSeconds     int     `json:"window_seconds,omitempty"`
	StartState        string  `json:"start_state,omitempty"`
	VerificationDueAt string  `json:"verification_due_at,omitempty"`
}

type creditsJSON struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance,omitempty"`
}

type resetCreditsJSON struct {
	AvailableCount int               `json:"available_count"`
	Credits        []resetCreditJSON `json:"credits,omitempty"`
}

type resetCreditJSON struct {
	Status     string `json:"status,omitempty"`
	GrantedAt  string `json:"granted_at,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	RedeemedAt string `json:"redeemed_at,omitempty"`
}

func newStatusJSON(u *usage.Usage, verbose bool) statusJSON {
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
	if u.Verification != nil {
		addStartJSON(s.FiveHour, u.Verification.FiveHour)
		addStartJSON(s.Weekly, u.Verification.Weekly)
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
	if verbose && json.Valid(u.Raw) {
		s.Raw = json.RawMessage(u.Raw)
	}
	return s
}

func addStartJSON(w *windowJSON, s usage.StartStatus) {
	if w == nil {
		return
	}
	w.StartState = s.State
	if !s.DueAt.IsZero() {
		w.VerificationDueAt = s.DueAt.Format(time.RFC3339)
	}
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
		AvailableCount: rc.AvailableCount,
		Credits:        make([]resetCreditJSON, 0, len(rc.Credits)),
	}
	for _, c := range rc.Credits {
		out.Credits = append(out.Credits, resetCreditJSON{
			Status:     c.Status,
			GrantedAt:  timeJSON(c.GrantedAt),
			ExpiresAt:  timeJSON(c.ExpiresAt),
			RedeemedAt: timeJSON(c.RedeemedAt),
		})
	}
	return out
}

func timeJSON(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func printUsage(out io.Writer, text cliText, u *usage.Usage, verbose bool, display string) {
	display = normalizeUsageDisplay(display)
	plan := u.Plan
	if plan != "" {
		plan = " (" + plan + ")"
	}
	fmt.Fprintf(out, "%s%s\n", u.Provider, plan)
	five, week := fmtWindow(text, u.FiveHour, display), fmtWindow(text, u.Weekly, display)
	if v := u.Verification; v != nil {
		if !u.FiveHour.Missing() {
			five += " — " + startDescription(text, v.FiveHour)
		}
		if !u.Weekly.Missing() {
			week += " — " + startDescription(text, v.Weekly)
		}
	}
	fmt.Fprintf(out, text.statusFiveHourLineFmt, five)
	fmt.Fprintf(out, text.statusWeeklyLineFmt, week)
	if v := u.Verification; v != nil {
		switch v.Recovery {
		case "verifying":
			fmt.Fprintln(out, "  "+text.verifyStatusCheck)
		case "backoff", "cooldown":
			fmt.Fprintf(out, "  "+text.verifyRetryFmt+"\n", fmtClock(text, v.NextEligible))
		case "ping_running":
			fmt.Fprintln(out, "  "+text.verifyRunning)
		case "ready":
			fmt.Fprintln(out, "  "+text.verifyReady)
		case "unavailable":
			fmt.Fprintln(out, "  "+text.verifyUnavailable)
		}
		if v.Warning != "" {
			fmt.Fprintln(out, "  "+v.Warning)
		}
	}
	if u.Credits != nil && (u.Credits.HasCredits || u.Credits.Unlimited) {
		if u.Credits.Unlimited {
			fmt.Fprint(out, text.statusCreditsUnlimited)
		} else {
			fmt.Fprintf(out, text.statusCreditsFmt, u.Credits.Balance)
		}
	}
	printResetCredits(out, text, u.ResetCredits)
	if verbose {
		fmt.Fprintf(out, "  raw: %s\n", string(u.Raw))
	}
	fmt.Fprintln(out)
}

func printResetCredits(out io.Writer, text cliText, rc *usage.ResetCredits) {
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
	default:
		return status
	}
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
