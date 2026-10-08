package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

func TestNextRedeemableCreditIgnoresTheTimingPolicy(t *testing.T) {
	now := time.Now()
	// Far from expiry, so the automatic policy would decline — an explicit
	// `redeem` still spends it.
	u := &usage.Usage{ResetCredits: &usage.ResetCredits{Credits: []usage.ResetCredit{
		{Status: "available", ExpiresAt: now.Add(20 * 24 * time.Hour)},
		{Status: "redeemed", ExpiresAt: now.Add(time.Hour), RedeemedAt: now},
	}}}
	if _, ok := u.ResetCreditToRedeem(now); ok {
		t.Fatal("precondition: the auto policy should decline this credit")
	}
	got, ok := nextRedeemableCredit(u, now)
	if !ok || !got.ExpiresAt.Equal(now.Add(20*24*time.Hour)) {
		t.Fatalf("nextRedeemableCredit() = %v/%t, want the available credit", got.ExpiresAt, ok)
	}
}

func TestNextRedeemableCreditFallsBackToTheCount(t *testing.T) {
	// The detail endpoint is private; when only the count survives, redeeming
	// must still be possible.
	u := &usage.Usage{ResetCredits: &usage.ResetCredits{AvailableCount: 1}}
	credit, ok := nextRedeemableCredit(u, time.Now())
	if !ok {
		t.Fatal("nextRedeemableCredit() = false, want the count to be trusted")
	}
	if !credit.ExpiresAt.IsZero() {
		t.Fatalf("credit = %+v, want an unknown expiry", credit)
	}

	none := &usage.Usage{ResetCredits: &usage.ResetCredits{}}
	if _, ok := nextRedeemableCredit(none, time.Now()); ok {
		t.Fatal("nextRedeemableCredit() = true with no credits at all")
	}
}

func TestRedeemOutcomeTextCoversEveryBackendOutcome(t *testing.T) {
	for _, outcome := range []string{
		provider.RedeemReset,
		provider.RedeemNothingToReset,
		provider.RedeemNoCredit,
		provider.RedeemAlreadyRedeemed,
		provider.RedeemCooldown,
		provider.RedeemIneligible,
	} {
		for _, text := range []cliText{enText, zhText} {
			got := redeemOutcomeText(text, provider.RedeemResult{Outcome: outcome})
			if got == "" || got == outcome {
				t.Fatalf("redeemOutcomeText(%q) = %q, want a translated sentence", outcome, got)
			}
		}
	}
	// An outcome we don't know about must surface as-is rather than read as a
	// successful redemption.
	if got := redeemOutcomeText(enText, provider.RedeemResult{Outcome: "brand_new_code"}); !strings.Contains(got, "brand_new_code") {
		t.Fatalf("unknown outcome = %q, want the raw code reported", got)
	}
}

func TestRedeemOutcomeTextUsesTheBackendsReason(t *testing.T) {
	retry := time.Date(2026, 10, 8, 12, 30, 0, 0, time.Local)
	cases := []struct {
		res  provider.RedeemResult
		want string
	}{
		{provider.RedeemResult{Outcome: provider.RedeemIneligible, Reason: "cli_version"}, "did not recognize limitping as the Claude CLI (cli_version)"},
		{provider.RedeemResult{Outcome: provider.RedeemIneligible, Reason: "not_next_grant"}, "can't be used right now (not_next_grant)"},
		{provider.RedeemResult{Outcome: provider.RedeemIneligible}, enText.redeemIneligible},
		{provider.RedeemResult{Outcome: provider.RedeemCooldown, RetryAt: retry}, "try again after Oct 08 12:30"},
		{provider.RedeemResult{Outcome: provider.RedeemCooldown}, enText.redeemCooldown},
		{provider.RedeemResult{Outcome: "brand_new_code", Reason: "why"}, "brand_new_code (why)"},
	}
	for _, tc := range cases {
		if got := redeemOutcomeText(enText, tc.res); !strings.Contains(got, tc.want) {
			t.Fatalf("redeemOutcomeText(%+v) = %q, want it to contain %q", tc.res, got, tc.want)
		}
		if got := redeemOutcomeText(zhText, tc.res); got == "" {
			t.Fatalf("zh redeemOutcomeText(%+v) is empty", tc.res)
		}
	}
}
