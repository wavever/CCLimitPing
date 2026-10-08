// Package usage defines the normalized rate-limit usage model shared across
// providers. Readers translate each provider's raw API response into these
// types so the scheduler and CLI can treat every provider uniformly.
package usage

import (
	"sort"
	"time"
)

// Window is a single rate-limit window (e.g. the 5h rolling window or the
// weekly window). UsedPercent is 0..100.
type Window struct {
	UsedPercent   float64
	ResetsAt      time.Time
	WindowSeconds int
}

// Active reports whether the window is currently running: a request has started
// it and it has not yet reset. This is the signal the scheduler uses to decide
// whether to ping immediately.
//
// Consumption deliberately plays no part. A window that a limitping ping is the
// only thing to have touched reports 0% used — Codex rounds used_percent to
// whole numbers, so a ~20k-token ping is 0, and Claude's utilization is just as
// coarse — so requiring consumption made the scheduler blind to the very window
// it had just started, and it re-pinged on a schedule that drifted a little
// further every cycle.
//
// A reset time is therefore the whole signal, and readers are responsible for
// only setting one on a window that a request actually started: a provider that
// reports an idle window as a full-length one must have that normalized away
// (see codexWindowAnchored), or watch would wait out a window that never began.
func (w Window) Active() bool {
	return !w.ResetsAt.IsZero() && time.Now().Before(w.ResetsAt)
}

// Missing reports whether the provider returned no data for this window at
// all, meaning the limit is not currently enforced (OpenAI temporarily removed
// Codex's 5h window on 2026-07-12, leaving only the weekly cap). Distinct from
// an inactive window, which is enforced but has no consumption yet.
func (w Window) Missing() bool {
	return w.UsedPercent == 0 && w.ResetsAt.IsZero() && w.WindowSeconds == 0
}

// Remaining returns the time until this window resets (never negative).
func (w Window) Remaining() time.Duration {
	if w.ResetsAt.IsZero() {
		return 0
	}
	d := time.Until(w.ResetsAt)
	if d < 0 {
		return 0
	}
	return d
}

// Credits describes pay-as-you-go credits that may remain available even when
// the weekly window is exhausted.
type Credits struct {
	HasCredits bool
	Unlimited  bool
	Balance    string
}

// Windows a reset can restore, as named in ResetCredit.Clears.
const (
	ClearsFiveHour = "five_hour"
	ClearsWeekly   = "seven_day"
)

// ResetCredit is a banked rate-limit reset: a Codex reset credit, or a Claude
// reset grant (the "reset card" Anthropic hands out, e.g. at a model launch).
//
// A zero ExpiresAt means the reset never lapses. Codex credits always carry an
// expiry; a Claude grant may not.
type ResetCredit struct {
	Status     string
	GrantedAt  time.Time
	ExpiresAt  time.Time
	RedeemedAt time.Time

	// ID names the reset to the backend, and Label is its display title; either
	// may be empty when the backend does not say.
	ID    string
	Label string
	// The fields below describe a Claude grant and stay zero for Codex.
	// Left and Total count the resets the grant holds; one grant may carry
	// several. Zero means a single-use credit.
	Left  int
	Total int
	// Clears lists the windows a reset restores (ClearsFiveHour, ClearsWeekly,
	// or a provider-specific name). Empty means every window.
	Clears []string
	// RequiresLimit means the backend only honors the reset while a window it
	// clears is actually at its limit.
	RequiresLimit bool
	// PercentUsed is the backend's own reading (0..100) of each window the
	// reset clears, under the names Clears uses, and Blocking lists the ones it
	// says are holding the account up right now. Both are readings: they only
	// mean something next to the windows they were read with, so they are
	// dropped when the credits are carried over to a later poll (see
	// ResetCredits.WithoutReadings). Codex gives neither.
	PercentUsed map[string]float64
	Blocking    []string
}

// Redeemable reports whether c can still be spent at now.
func (c ResetCredit) Redeemable(now time.Time) bool {
	if !c.RedeemedAt.IsZero() || c.Expired(now) {
		return false
	}
	return c.Status == "" || c.Status == "available"
}

// Expired reports whether c has lapsed by now. A reset without an expiry never
// does.
func (c ResetCredit) Expired(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt)
}

// ClearsWindow reports whether a reset restores the named window.
func (c ResetCredit) ClearsWindow(window string) bool {
	if len(c.Clears) == 0 {
		return true
	}
	for _, w := range c.Clears {
		if w == window {
			return true
		}
	}
	return false
}

// ExpiresBefore orders resets soonest-lapsing first, with the ones that never
// lapse last, so "spend the one about to be lost" is a single comparison.
func (c ResetCredit) ExpiresBefore(other ResetCredit) bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return other.ExpiresAt.IsZero() || c.ExpiresAt.Before(other.ExpiresAt)
}

// ResetCredits summarizes a provider's banked resets.
type ResetCredits struct {
	AvailableCount int
	Credits        []ResetCredit

	// LimitState says the backend reported which of its limits are hit, so
	// AtLimit and Exhausted are its own word rather than something to infer
	// from the window readings. Exhausted names the windows at their limit (as
	// Clears names them); AtLimit is the backend's overall verdict. Like a
	// credit's PercentUsed, these are readings.
	LimitState bool
	AtLimit    bool
	Exhausted  []string
	// CooldownUntil is when the backend next accepts a reset, when it has said
	// it will not before then. Being a deadline rather than a reading, it stays
	// valid when the credits are carried over.
	CooldownUntil time.Time
	// UnavailableReason is the backend's reason for withholding resets from
	// this caller altogether (Claude's ineligible_reason), set only when that
	// reason tells the user something. Credits is empty then.
	UnavailableReason string
}

// WithoutReadings copies rc minus everything that describes the moment it was
// read — the backend's limit verdict and its per-credit usage readings — for a
// poll that carries cached credits alongside fresher windows: a verdict an hour
// old must not overrule the windows read just now.
func (rc *ResetCredits) WithoutReadings() *ResetCredits {
	if rc == nil {
		return nil
	}
	out := *rc
	out.LimitState, out.AtLimit, out.Exhausted = false, false, nil
	out.Credits = make([]ResetCredit, len(rc.Credits))
	for i, c := range rc.Credits {
		c.PercentUsed, c.Blocking = nil, nil
		out.Credits[i] = c
	}
	return &out
}

// ScopedLimit is a limit narrower than the plan-wide windows — Claude's weekly
// cap on one model or one surface — under the provider's own label. It is
// informational: a ping only needs the plan-wide 5h window to start.
type ScopedLimit struct {
	Label  string
	Window Window
}

// Usage is a provider's full rate-limit snapshot at FetchedAt.
type Usage struct {
	Provider     string
	FiveHour     Window
	Weekly       Window
	ScopedLimits []ScopedLimit
	// Buckets holds provider-specific windows under the provider's own names
	// (Claude's seven_day_opus, say) — the names a ResetCredit's Clears uses —
	// so a reset that only restores one of them can be valued.
	Buckets      map[string]Window
	Plan         string
	Credits      *Credits
	ResetCredits *ResetCredits
	// ResetCreditsError is why the reset credits could not be read, when they
	// could not: ResetCredits is then empty, or carried over from an earlier
	// read, and says nothing reliable about what is held now.
	ResetCreditsError error
	LimitReached      bool
	FetchedAt         time.Time
	Raw               []byte // raw JSON body, for `status -v`
}

// Reset-credit auto-redeem policy. A credit that lapses unused is worth
// nothing, but redeeming one while the windows are near-empty reclaims nothing
// either — so a credit is only spent close to expiry: while there is real
// consumption to win back, or, in the final hour, unconditionally. The backend
// answers "nothing to reset" when no window is actually eligible, so that
// last-hour attempt cannot burn a credit for nothing. A reset that never
// expires is never under that pressure, so the policy leaves it alone.
//
// A reset the backend only honors at a limit (RequiresLimit) waits for one of
// its windows to actually be at it: before that every attempt is refused, and
// each refusal is a request sent for nothing.
const (
	redeemExpirySoon     = 24 * time.Hour
	redeemLastChance     = time.Hour
	redeemUsedPercentMin = 50
	// atLimitPercent is where a window counts as at its limit. Claude can
	// report 99.x at the cap rather than 100.
	atLimitPercent = 99
)

// ResetCreditToRedeem returns the soonest-expiring reset credit that should be
// spent now, if any.
func (u *Usage) ResetCreditToRedeem(now time.Time) (ResetCredit, bool) {
	if u.ResetCredits == nil {
		return ResetCredit{}, false
	}
	// The backend has said when it next accepts one; asking sooner is refused.
	if now.Before(u.ResetCredits.CooldownUntil) {
		return ResetCredit{}, false
	}
	var target ResetCredit
	found := false
	for _, c := range u.ResetCredits.Credits {
		if !c.Redeemable(now) || c.ExpiresAt.IsZero() {
			continue
		}
		if !found || c.ExpiresBefore(target) {
			target, found = c, true
		}
	}
	if !found {
		return ResetCredit{}, false
	}
	if target.RequiresLimit && !u.AtLimit(target) {
		return ResetCredit{}, false
	}
	remaining := target.ExpiresAt.Sub(now)
	if remaining <= redeemLastChance || (remaining <= redeemExpirySoon && u.WorthRedeeming(target)) {
		return target, true
	}
	return ResetCredit{}, false
}

// WorthRedeeming reports whether a window c restores has consumed enough that
// the reset would actually give something back.
func (u *Usage) WorthRedeeming(c ResetCredit) bool {
	return anyUsedAtLeast(u.ClearedUsage(c), redeemUsedPercentMin)
}

// AtLimit reports whether a window c restores is at its limit — the only time
// a reset with RequiresLimit is honored. The backend's own verdict decides when
// it gave one; otherwise the usage readings are judged, and with no reading for
// any of the windows the provider's own limit-reached flag is the best
// evidence there is.
func (u *Usage) AtLimit(c ResetCredit) bool {
	if hit, known := u.reportedAtLimit(c); known {
		return hit
	}
	readings := u.ClearedUsage(c)
	if len(readings) == 0 {
		return u.LimitReached
	}
	return anyUsedAtLeast(readings, atLimitPercent)
}

// reportedAtLimit is the backend's word on whether c can be used at a limit
// right now, read the way Claude Code reads it: a window c clears is exhausted,
// or blocking the account. A verdict of "at a limit" that names no window is
// taken as is; a claim that then clears nothing is refused at no cost.
func (u *Usage) reportedAtLimit(c ResetCredit) (hit, known bool) {
	for _, w := range c.Blocking {
		if c.ClearsWindow(w) {
			return true, true
		}
	}
	rc := u.ResetCredits
	if rc == nil || !rc.LimitState {
		return false, false
	}
	for _, w := range rc.Exhausted {
		if c.ClearsWindow(w) {
			return true, true
		}
	}
	return rc.AtLimit && len(rc.Exhausted) == 0, true
}

// ClearedUsage is how used each window c restores is: the backend's own
// readings for the reset when it gave them — they are what it judges a claim
// by — else u's readings of those windows (ClearedWindows).
func (u *Usage) ClearedUsage(c ResetCredit) []NamedWindow {
	if len(c.PercentUsed) == 0 {
		return u.ClearedWindows(c)
	}
	names := make([]string, 0, len(c.PercentUsed))
	for name := range c.PercentUsed {
		names = append(names, name)
	}
	// The plan-wide windows first, as ClearedWindows orders them.
	sort.Slice(names, func(i, j int) bool {
		if ri, rj := windowRank(names[i]), windowRank(names[j]); ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})
	out := make([]NamedWindow, 0, len(names))
	for _, name := range names {
		out = append(out, NamedWindow{name, Window{UsedPercent: c.PercentUsed[name]}})
	}
	return out
}

func windowRank(name string) int {
	switch name {
	case ClearsFiveHour:
		return 0
	case ClearsWeekly:
		return 1
	default:
		return 2
	}
}

func anyUsedAtLeast(windows []NamedWindow, percent float64) bool {
	for _, w := range windows {
		if w.Window.UsedPercent >= percent {
			return true
		}
	}
	return false
}

// ClearedWindows lists, in a stable order, the windows c restores that u has a
// reading for: the plan-wide ones, then provider-specific buckets such as a
// per-model weekly limit.
func (u *Usage) ClearedWindows(c ResetCredit) []NamedWindow {
	var out []NamedWindow
	for _, w := range []NamedWindow{{ClearsFiveHour, u.FiveHour}, {ClearsWeekly, u.Weekly}} {
		if c.ClearsWindow(w.Name) && !w.Window.Missing() {
			out = append(out, w)
		}
	}
	names := make([]string, 0, len(u.Buckets))
	for name := range u.Buckets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if c.ClearsWindow(name) {
			out = append(out, NamedWindow{name, u.Buckets[name]})
		}
	}
	return out
}

// NamedWindow is a window under the name a ResetCredit's Clears uses for it.
type NamedWindow struct {
	Name   string
	Window Window
}

// CreditsUsable reports whether credits can cover a request when the weekly
// window is exhausted.
func (u *Usage) CreditsUsable() bool {
	return u.Credits != nil && (u.Credits.Unlimited || u.Credits.HasCredits)
}

// WeeklyExhausted reports whether the weekly window should block new work: its
// utilization is at/above threshold (0..1, e.g. cfg.WeeklyThreshold) and no
// usable credits remain. Shared by the scheduler's ping skip and the continue
// proxy's recovery gate so "weekly is spent" means the same thing everywhere.
func (u *Usage) WeeklyExhausted(threshold float64) bool {
	if u.CreditsUsable() {
		return false
	}
	return u.Weekly.UsedPercent/100 >= threshold
}
