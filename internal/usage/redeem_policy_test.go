package usage

import (
	"testing"
	"time"
)

func expiringIn(d time.Duration, now time.Time) ResetCredit {
	return ResetCredit{Status: "available", ExpiresAt: now.Add(d)}
}

// A reset the backend only honors at a limit is refused below it, so trying
// earlier only sends requests that cannot succeed.
func TestResetCreditToRedeemWaitsForTheLimitWhenRequired(t *testing.T) {
	now := time.Now()
	c := expiringIn(2*time.Hour, now)
	c.RequiresLimit = true
	u := &Usage{
		FiveHour:     Window{UsedPercent: 60},
		ResetCredits: &ResetCredits{Credits: []ResetCredit{c}},
	}
	if _, ok := u.ResetCreditToRedeem(now); ok {
		t.Fatal("redeemed a limit-only reset at 60%")
	}
	u.FiveHour.UsedPercent = 99.4 // Claude can sit just under 100 at the cap
	if _, ok := u.ResetCreditToRedeem(now); !ok {
		t.Fatal("did not redeem a limit-only reset at the limit")
	}

	// Even the last-hour attempt waits for the limit.
	u.FiveHour.UsedPercent = 10
	u.ResetCredits.Credits[0].ExpiresAt = now.Add(30 * time.Minute)
	if _, ok := u.ResetCreditToRedeem(now); ok {
		t.Fatal("the last-chance attempt fired for a limit-only reset below the limit")
	}
}

// A reset that only restores a provider-specific window (one model's weekly
// cap) is valued by that window, not by the plan-wide ones.
func TestResetCreditValuedByTheBucketItClears(t *testing.T) {
	now := time.Now()
	c := expiringIn(2*time.Hour, now)
	c.Clears = []string{"seven_day_opus"}
	u := &Usage{
		FiveHour:     Window{UsedPercent: 5},
		Weekly:       Window{UsedPercent: 5},
		Buckets:      map[string]Window{"seven_day_opus": {UsedPercent: 100}, "seven_day_sonnet": {UsedPercent: 0}},
		ResetCredits: &ResetCredits{Credits: []ResetCredit{c}},
	}
	if !u.WorthRedeeming(c) || !u.AtLimit(c) {
		t.Fatal("the Opus cap at 100% should make the reset worth spending")
	}
	if got := u.ClearedWindows(c); len(got) != 1 || got[0].Name != "seven_day_opus" {
		t.Fatalf("cleared windows = %+v, want only the Opus bucket", got)
	}
	if _, ok := u.ResetCreditToRedeem(now); !ok {
		t.Fatal("did not redeem a reset whose window is at its limit")
	}
}

// With no reading for any window a limit-only reset clears, the provider's own
// limit flag decides.
func TestAtLimitFallsBackToLimitReached(t *testing.T) {
	c := ResetCredit{Clears: []string{"seven_day_cowork"}, RequiresLimit: true}
	u := &Usage{}
	if u.AtLimit(c) {
		t.Fatal("no reading and no limit flag is not at a limit")
	}
	u.LimitReached = true
	if !u.AtLimit(c) {
		t.Fatal("the limit flag should count when nothing else can be read")
	}
}

// When the backend says which limits are hit, its word decides — over the
// window readings either way.
func TestAtLimitPrefersTheBackendsVerdict(t *testing.T) {
	card := ResetCredit{Clears: []string{ClearsFiveHour, ClearsWeekly}, RequiresLimit: true}
	cases := []struct {
		name      string
		fiveHour  float64
		rc        ResetCredits
		blocking  []string
		wantLimit bool
	}{
		{"exhausted window it clears", 40, ResetCredits{LimitState: true, AtLimit: true, Exhausted: []string{ClearsWeekly}}, nil, true},
		{"exhausted window it does not clear", 40, ResetCredits{LimitState: true, AtLimit: true, Exhausted: []string{"seven_day_opus"}}, nil, false},
		{"readings at 100 but nothing exhausted", 100, ResetCredits{LimitState: true, Exhausted: []string{}}, nil, false},
		{"at a limit, no window named", 10, ResetCredits{LimitState: true, AtLimit: true}, nil, true},
		{"a cleared window blocking", 10, ResetCredits{}, []string{ClearsFiveHour}, true},
		{"no verdict: the readings decide", 99.5, ResetCredits{}, nil, true},
		{"no verdict, low readings", 50, ResetCredits{}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := card
			c.Blocking = tc.blocking
			rc := tc.rc
			u := &Usage{FiveHour: Window{UsedPercent: tc.fiveHour, WindowSeconds: 18000}, Weekly: Window{UsedPercent: 5, WindowSeconds: 604800}, ResetCredits: &rc}
			if got := u.AtLimit(c); got != tc.wantLimit {
				t.Fatalf("AtLimit = %t, want %t", got, tc.wantLimit)
			}
		})
	}
}

// The grant's own reading of its windows is what the backend judges a claim
// by, so it is what the value of spending it is judged by too.
func TestWorthRedeemingPrefersTheCreditsOwnReadings(t *testing.T) {
	c := ResetCredit{Clears: []string{ClearsFiveHour, ClearsWeekly}, PercentUsed: map[string]float64{ClearsFiveHour: 70, ClearsWeekly: 2}}
	u := &Usage{FiveHour: Window{UsedPercent: 5, WindowSeconds: 18000}, Weekly: Window{UsedPercent: 5, WindowSeconds: 604800}}
	if !u.WorthRedeeming(c) {
		t.Fatal("the credit's own 70% reading should make it worth spending")
	}
	got := u.ClearedUsage(c)
	if len(got) != 2 || got[0].Name != ClearsFiveHour || got[0].Window.UsedPercent != 70 || got[1].Name != ClearsWeekly {
		t.Fatalf("cleared usage = %+v, want the credit's readings, 5h first", got)
	}
	c.PercentUsed = map[string]float64{ClearsFiveHour: 10}
	u.FiveHour.UsedPercent = 90 // the window reading is ignored when the credit has its own
	if u.WorthRedeeming(c) {
		t.Fatal("judged by the window reading although the credit carried its own")
	}
	c.PercentUsed = nil
	if !u.WorthRedeeming(c) {
		t.Fatal("without the credit's readings the windows should decide")
	}
}

func TestResetCreditToRedeemWaitsOutTheCooldown(t *testing.T) {
	now := time.Now()
	u := &Usage{
		FiveHour:     Window{UsedPercent: 90, WindowSeconds: 18000},
		ResetCredits: &ResetCredits{Credits: []ResetCredit{expiringIn(30*time.Minute, now)}, CooldownUntil: now.Add(10 * time.Minute)},
	}
	if _, ok := u.ResetCreditToRedeem(now); ok {
		t.Fatal("tried a reset during the backend's cooldown")
	}
	if _, ok := u.ResetCreditToRedeem(now.Add(11 * time.Minute)); !ok {
		t.Fatal("did not try once the cooldown was over")
	}
}

// Carried over to a later poll, credits keep what they are but drop what was
// read with them.
func TestWithoutReadingsKeepsTheCreditsButNotTheVerdict(t *testing.T) {
	cooldown := time.Now().Add(time.Hour)
	rc := &ResetCredits{
		AvailableCount: 1,
		LimitState:     true, AtLimit: true, Exhausted: []string{ClearsFiveHour},
		CooldownUntil: cooldown,
		Credits:       []ResetCredit{{ID: "g", Left: 1, PercentUsed: map[string]float64{ClearsFiveHour: 100}, Blocking: []string{ClearsFiveHour}}},
	}
	got := rc.WithoutReadings()
	if got.LimitState || got.AtLimit || got.Exhausted != nil || got.Credits[0].PercentUsed != nil || got.Credits[0].Blocking != nil {
		t.Fatalf("WithoutReadings = %+v, want every reading dropped", got)
	}
	if got.AvailableCount != 1 || got.Credits[0].ID != "g" || !got.CooldownUntil.Equal(cooldown) {
		t.Fatalf("WithoutReadings = %+v, want the credits and the cooldown kept", got)
	}
	// The cached original is untouched.
	if !rc.LimitState || rc.Credits[0].PercentUsed == nil {
		t.Fatal("WithoutReadings changed the credits it copied")
	}
	if (*ResetCredits)(nil).WithoutReadings() != nil {
		t.Fatal("nil credits should stay nil")
	}
}
