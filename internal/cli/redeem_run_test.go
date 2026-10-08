package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

// fakeRedeemer is a provider holding the given resets, recording what it was
// asked to spend.
type fakeRedeemer struct {
	name    string
	u       *usage.Usage
	readErr error
	outcome string
	reason  string
	spent   []usage.ResetCredit
}

func (f *fakeRedeemer) Name() string { return f.name }
func (f *fakeRedeemer) ReadUsage(context.Context) (*usage.Usage, error) {
	return f.u, f.readErr
}
func (f *fakeRedeemer) Trigger(context.Context, bool) (*provider.TriggerResult, error) {
	return nil, errors.New("not used")
}
func (f *fakeRedeemer) RedeemResetCredit(_ context.Context, c usage.ResetCredit) (provider.RedeemResult, error) {
	f.spent = append(f.spent, c)
	return provider.RedeemResult{Outcome: f.outcome, Reason: f.reason}, nil
}
func (f *fakeRedeemer) AutoRedeemResetCredit(context.Context, *usage.Usage) (provider.RedeemResult, error) {
	return provider.RedeemResult{}, nil
}

// holding is an account holding credits, at 80% of its 5h window so that
// spending one is worth it.
func holding(credits ...usage.ResetCredit) *usage.Usage {
	return &usage.Usage{
		FiveHour:     usage.Window{UsedPercent: 80, WindowSeconds: 18000},
		Weekly:       usage.Window{UsedPercent: 10, WindowSeconds: 604800},
		ResetCredits: &usage.ResetCredits{AvailableCount: len(credits), Credits: credits},
	}
}

func TestRunRedeemRefusesToWasteAResetOnBarelyUsedWindows(t *testing.T) {
	card := usage.ResetCredit{Status: "available", ID: "g", Clears: []string{usage.ClearsFiveHour, usage.ClearsWeekly}}
	u := holding(card)
	u.FiveHour.UsedPercent, u.Weekly.UsedPercent = 22, 4
	claude := &fakeRedeemer{name: "claude", u: u, outcome: provider.RedeemReset}
	ps := []provider.Provider{claude}

	err := runRedeem(context.Background(), &bytes.Buffer{}, enText, ps, false, false)
	if err == nil || !strings.Contains(err.Error(), "5h 22%, weekly 4%") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a refusal naming the usage and --force", err)
	}
	if len(claude.spent) != 0 {
		t.Fatal("a barely used window was reset without --force")
	}

	// The dry run warns instead of refusing.
	var out bytes.Buffer
	if err := runRedeem(context.Background(), &out, enText, ps, true, false); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out.String(), "note: only 5h 22%, weekly 4% used") {
		t.Fatalf("dry run output = %q, want the low-usage note", out.String())
	}

	if err := runRedeem(context.Background(), &bytes.Buffer{}, enText, ps, false, true); err != nil {
		t.Fatalf("--force: %v", err)
	}
	if len(claude.spent) != 1 {
		t.Fatal("--force did not spend the reset")
	}
}

func TestRunRedeemJudgesOnlyTheWindowsTheResetRestores(t *testing.T) {
	// Weekly is nearly spent, but this card only restores the 5h window.
	card := usage.ResetCredit{Status: "available", ID: "g", Clears: []string{usage.ClearsFiveHour}}
	u := holding(card)
	u.FiveHour.UsedPercent, u.Weekly.UsedPercent = 10, 95
	claude := &fakeRedeemer{name: "claude", u: u}

	err := runRedeem(context.Background(), &bytes.Buffer{}, enText, []provider.Provider{claude}, false, false)
	if err == nil || !strings.Contains(err.Error(), "only 5h 10% used") {
		t.Fatalf("err = %v, want a refusal that names only the 5h window", err)
	}
}

func TestRunRedeemSpendsTheCardItShowed(t *testing.T) {
	card := usage.ResetCredit{Status: "available", ID: "opus55-launch", Label: "Opus 5.5 launch", ExpiresAt: time.Now().Add(48 * time.Hour)}
	claude := &fakeRedeemer{name: "claude", u: holding(card), outcome: provider.RedeemReset}
	codex := &fakeRedeemer{name: "codex", u: holding()}

	var out bytes.Buffer
	if err := runRedeem(context.Background(), &out, enText, []provider.Provider{claude, codex}, false, false); err != nil {
		t.Fatalf("runRedeem: %v", err)
	}
	if len(claude.spent) != 1 || claude.spent[0].ID != "opus55-launch" {
		t.Fatalf("spent = %+v, want the card that was shown", claude.spent)
	}
	got := out.String()
	for _, want := range []string{"claude  redeeming 1 reset credit — Opus 5.5 launch (expires", "claude  redeemed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want it to contain %q", got, want)
		}
	}
}

func TestRunRedeemAsksWhichProviderWhenBothHoldResets(t *testing.T) {
	claude := &fakeRedeemer{name: "claude", u: holding(usage.ResetCredit{Status: "available", ID: "g"})}
	codex := &fakeRedeemer{name: "codex", u: holding(usage.ResetCredit{Status: "available", ExpiresAt: time.Now().Add(time.Hour)})}
	ps := []provider.Provider{claude, codex}

	err := runRedeem(context.Background(), &bytes.Buffer{}, enText, ps, false, false)
	if err == nil || !strings.Contains(err.Error(), "claude, codex") {
		t.Fatalf("err = %v, want a request to name a provider", err)
	}
	if len(claude.spent)+len(codex.spent) != 0 {
		t.Fatal("an ambiguous redeem spent a reset")
	}

	// A dry run spends nothing, so it can show both.
	var out bytes.Buffer
	if err := runRedeem(context.Background(), &out, enText, ps, true, false); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out.String(), "claude ") || !strings.Contains(out.String(), "codex ") {
		t.Fatalf("dry run output = %q, want both plans", out.String())
	}
}

func TestRunRedeemSurvivesOneProviderFailingToRead(t *testing.T) {
	claude := &fakeRedeemer{name: "claude", readErr: errors.New("not logged in")}
	codex := &fakeRedeemer{name: "codex", u: holding(usage.ResetCredit{Status: "available", ExpiresAt: time.Now().Add(time.Hour)}), outcome: provider.RedeemReset}
	if err := runRedeem(context.Background(), &bytes.Buffer{}, enText, []provider.Provider{claude, codex}, false, false); err != nil {
		t.Fatalf("runRedeem: %v", err)
	}
	if len(codex.spent) != 1 {
		t.Fatal("the provider that could be read did not redeem")
	}

	// With nothing to redeem anywhere, the read failure is the answer.
	codex.u = holding()
	err := runRedeem(context.Background(), &bytes.Buffer{}, enText, []provider.Provider{claude, codex}, false, false)
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v, want the read failure reported", err)
	}
}

// "No reset credits available" is only true when the provider read them and
// holds none; every other reason for having nothing to spend is said as such.
func TestRunRedeemSaysWhyNothingCanBeSpent(t *testing.T) {
	cases := []struct {
		name string
		u    *usage.Usage
		want []string
	}{
		{"cards could not be read", &usage.Usage{ResetCreditsError: errors.New("the reset-card request was refused: HTTP 400")},
			[]string{"claude: the reset credits could not be read", "HTTP 400"}},
		{"cards held but none usable", holding(
			usage.ResetCredit{Status: provider.ClaudeGrantPaused, ID: "a"},
			usage.ResetCredit{Status: provider.ClaudeGrantQueued, ID: "b"},
			usage.ResetCredit{Status: "redeemed", ID: "c"}),
			[]string{"none can be used right now", "paused", "queued behind another card"}},
		{"withheld from the CLI", &usage.Usage{ResetCredits: &usage.ResetCredits{UnavailableReason: "cli_version"}},
			[]string{"did not recognize limitping as the Claude CLI (cli_version)", "on PATH"}},
		{"withheld from the account", &usage.Usage{ResetCredits: &usage.ResetCredits{UnavailableReason: "tier"}},
			[]string{"not offered to this account (tier)"}},
		{"genuinely none", holding(usage.ResetCredit{Status: "redeemed", ID: "c"}),
			[]string{enText.redeemNoneAvailable}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claude := &fakeRedeemer{name: "claude", u: tc.u, outcome: provider.RedeemReset}
			err := runRedeem(context.Background(), &bytes.Buffer{}, enText, []provider.Provider{claude}, false, true)
			if err == nil {
				t.Fatal("want an error: nothing can be spent")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %q, want it to contain %q", err, want)
				}
			}
			if len(claude.spent) != 0 {
				t.Fatal("spent a reset that could not be used")
			}
		})
	}
}

func TestRunRedeemExplainsAnIneligibleCard(t *testing.T) {
	claude := &fakeRedeemer{name: "claude", u: holding(usage.ResetCredit{Status: "available", ID: "g"}),
		outcome: provider.RedeemIneligible, reason: "surface"}
	var out bytes.Buffer
	if err := runRedeem(context.Background(), &out, enText, []provider.Provider{claude}, false, true); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "did not recognize limitping as the Claude CLI (surface)") || strings.Contains(got, "any more") {
		t.Fatalf("output = %q, want the claim blamed on how limitping presented, not on the card", got)
	}
}

func TestNextRedeemableCreditPrefersTheResetThatLapses(t *testing.T) {
	now := time.Now()
	u := holding(
		usage.ResetCredit{Status: "available", ID: "forever"},
		usage.ResetCredit{Status: "available", ID: "dated", ExpiresAt: now.Add(72 * time.Hour)},
	)
	if got, ok := nextRedeemableCredit(u, now); !ok || got.ID != "dated" {
		t.Fatalf("nextRedeemableCredit() = %q/%t, want the one with an expiry", got.ID, ok)
	}
}
