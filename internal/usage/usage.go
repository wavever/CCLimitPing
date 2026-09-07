// Package usage defines the normalized rate-limit usage model shared across
// providers. Readers translate each provider's raw API response into these
// types so the scheduler and CLI can treat every provider uniformly.
package usage

import "time"

// Window is a single rate-limit window (e.g. the 5h rolling window or the
// weekly window). UsedPercent is 0..100.
type Window struct {
	UsedPercent   float64
	ResetsAt      time.Time
	WindowSeconds int
}

// Active reports whether the window currently has consumption recorded and has
// not yet reset. A freshly reset (or never-started) window is inactive, which
// is the signal the scheduler uses to decide whether to ping immediately.
func (w Window) Active() bool {
	return w.UsedPercent > 0 && !w.ResetsAt.IsZero() && time.Now().Before(w.ResetsAt)
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

// ResetCredit is a banked Codex rate-limit reset credit.
type ResetCredit struct {
	Status     string
	GrantedAt  time.Time
	ExpiresAt  time.Time
	RedeemedAt time.Time
}

// Redeemable reports whether c can still be spent at now.
func (c ResetCredit) Redeemable(now time.Time) bool {
	if !c.RedeemedAt.IsZero() || c.ExpiresAt.IsZero() || !now.Before(c.ExpiresAt) {
		return false
	}
	return c.Status == "" || c.Status == "available"
}

// ResetCredits summarizes account-backed Codex reset credits.
type ResetCredits struct {
	AvailableCount int
	Credits        []ResetCredit
}

// Usage is a provider's full rate-limit snapshot at FetchedAt.
type Usage struct {
	Provider     string
	FiveHour     Window
	Weekly       Window
	Plan         string
	Credits      *Credits
	ResetCredits *ResetCredits
	LimitReached bool
	FetchedAt    time.Time
	Raw          []byte        // raw JSON body, for `status -v`
	Verification *Verification // Codex-backed providers only; not a shared window predicate
	QuotaAccount string        // identity of the quota request; never part of status JSON
}

// Verification separates a completed CLI request from an observed quota window.
type Verification struct {
	FiveHour      StartStatus
	Weekly        StartStatus
	Target        string
	Recovery      string
	NextEligible  time.Time
	PreviousReset time.Time // known reset boundary of the target's previous window
	Warning       string
}

type StartStatus struct {
	State string
	DueAt time.Time
}

// Reset-credit auto-redeem policy. A credit that lapses unused is worth
// nothing, but redeeming one while the windows are near-empty reclaims nothing
// either — so a credit is only spent close to expiry: while there is real
// consumption to win back, or, in the final hour, unconditionally. The backend
// answers "nothing to reset" when no window is actually eligible, so that
// last-hour attempt cannot burn a credit for nothing.
const (
	redeemExpirySoon     = 24 * time.Hour
	redeemLastChance     = time.Hour
	redeemUsedPercentMin = 50
)

// ResetCreditToRedeem returns the soonest-expiring reset credit that should be
// spent now, if any.
func (u *Usage) ResetCreditToRedeem(now time.Time) (ResetCredit, bool) {
	if u.ResetCredits == nil {
		return ResetCredit{}, false
	}
	var target ResetCredit
	found := false
	for _, c := range u.ResetCredits.Credits {
		if !c.Redeemable(now) {
			continue
		}
		if !found || c.ExpiresAt.Before(target.ExpiresAt) {
			target, found = c, true
		}
	}
	if !found {
		return ResetCredit{}, false
	}
	remaining := target.ExpiresAt.Sub(now)
	if remaining <= redeemLastChance || (remaining <= redeemExpirySoon && u.worthReclaiming()) {
		return target, true
	}
	return ResetCredit{}, false
}

// worthReclaiming reports whether either window has consumed enough that a
// reset would actually give something back.
func (u *Usage) worthReclaiming() bool {
	return u.FiveHour.UsedPercent >= redeemUsedPercentMin || u.Weekly.UsedPercent >= redeemUsedPercentMin
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
