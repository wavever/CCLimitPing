package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

func TestRedeemAttemptsKeepTheKeyUntilAnswered(t *testing.T) {
	var a redeemAttempts
	first := a.key("credit")

	// Lost response / unconfirmed: the retry must be the same request.
	a.settle("credit", claimInDoubt)
	if a.key("credit") != first {
		t.Fatal("an attempt with no answer got a new key; a retry could spend a second reset")
	}

	// A definite refusal: the next try is a new attempt.
	a.settle("credit", claimAnswered)
	second := a.key("credit")
	if second == first {
		t.Fatal("a refused attempt kept its key; a backend that remembers keys would refuse forever")
	}
	if a.key("other") != (&redeemAttempts{}).key("other") {
		t.Fatal("attempts on one reset changed another's key")
	}

	// A request refused as such (a client error) is just as definite.
	a.settle("credit", claimRefused)
	if a.key("credit") == second {
		t.Fatal("a refused request kept its key; it would be refused the same way every cooldown")
	}
	// A claim that never left changes nothing.
	third := a.key("credit")
	a.settle("credit", claimNotSent)
	if a.key("credit") != third {
		t.Fatal("a claim that was never sent moved on to a new attempt")
	}
}

// End to end through Codex auto-redeem: nothingToReset, then the window fills,
// then the retry must carry a fresh key.
func TestCodexAutoRedeemRetriesARefusalUnderANewKey(t *testing.T) {
	fakeCodexHome(t)
	var keys []string
	fakeCodexAppServer(t, func(_ string, p any) (json.RawMessage, error) {
		keys = append(keys, p.(map[string]string)["idempotencyKey"])
		return json.RawMessage(`{"outcome":"nothingToReset"}`), nil
	})
	c := NewCodex(config.ProviderConfig{})
	u := &usage.Usage{
		FiveHour: usage.Window{UsedPercent: 80},
		ResetCredits: &usage.ResetCredits{Credits: []usage.ResetCredit{
			{ID: "c1", Status: "available", ExpiresAt: time.Now().Add(2 * time.Hour)},
		}},
	}
	for i := 0; i < 2; i++ {
		if _, err := c.AutoRedeemResetCredit(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		c.lastRedeem = time.Time{} // skip the cooldown
	}
	if len(keys) != 2 || keys[0] == keys[1] {
		t.Fatalf("keys = %q, want two distinct attempts", keys)
	}
}
