package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

// TestMain keeps the whole package off the real ~/.config/limitping and
// ~/.claude.json: a test that forgets to fake them records its claims in a
// throwaway dir, never the user's, and does not pick its ping directory from
// whatever the user happens to trust.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "limitping-provider-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func useClaimsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "limitping", "reset-claims")
}

func TestPendingClaimsRememberUntilSettled(t *testing.T) {
	dir := useClaimsDir(t)
	store := pendingClaims{provider: "claude"}
	now := time.Now()
	claim := pendingClaim{Account: "org", ResetsLeft: 2, RequestID: "req-1", At: now}

	if err := store.settle("g", claim, claimInDoubt); err != nil {
		t.Fatal(err)
	}
	got, ok := store.lookup("g", "org", now)
	if !ok || got.RequestID != "req-1" || got.ResetsLeft != 2 {
		t.Fatalf("lookup = %+v/%t, want the claim in doubt", got, ok)
	}
	path := filepath.Join(dir, "claude.json")
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}

	// Neither a refused request nor one never sent says anything about it.
	for _, s := range []claimSettlement{claimRefused, claimNotSent} {
		if err := store.settle("g", pendingClaim{Account: "org", RequestID: "other", At: now}, s); err != nil {
			t.Fatal(err)
		}
		if got, _ := store.lookup("g", "org", now); got.RequestID != "req-1" {
			t.Fatalf("settlement %v replaced the claim in doubt with %+v", s, got)
		}
	}

	if err := store.settle("g", claim, claimAnswered); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.lookup("g", "org", now); ok {
		t.Fatal("an answered claim is still remembered")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat = %v, want the empty record removed", err)
	}
}

func TestPendingClaimsLookupRejects(t *testing.T) {
	useClaimsDir(t)
	store := pendingClaims{provider: "claude"}
	now := time.Now()
	seed := func(key string, c pendingClaim) {
		t.Helper()
		if err := store.settle(key, c, claimInDoubt); err != nil {
			t.Fatal(err)
		}
	}
	seed("other-account", pendingClaim{Account: "org-a", RequestID: "r", At: now})
	seed("aged-out", pendingClaim{Account: "org", RequestID: "r", At: now.Add(-pendingClaimMaxAge - time.Hour)})
	seed("malformed", pendingClaim{Account: "org", RequestID: "has spaces", At: now})
	seed("from-the-future", pendingClaim{Account: "org", RequestID: "r", At: now.Add(48 * time.Hour)})

	for key, account := range map[string]string{
		"other-account":   "org-b", // never repeat a request for another login
		"aged-out":        "org",
		"malformed":       "org",
		"from-the-future": "org",
		"missing":         "org",
	} {
		if c, ok := store.lookup(key, account, now); ok {
			t.Fatalf("%s: lookup = %+v, want it ignored", key, c)
		}
	}
}

func TestPendingClaimsSurviveAnUnreadableFile(t *testing.T) {
	dir := useClaimsDir(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "codex.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := pendingClaims{provider: "codex"}
	if _, ok := store.lookup("k", "", time.Now()); ok {
		t.Fatal("a corrupt file yielded a claim")
	}
	if err := store.settle("k", pendingClaim{RequestID: "r", At: time.Now()}, claimInDoubt); err != nil {
		t.Fatalf("settle over a corrupt file: %v", err)
	}
	if _, ok := store.lookup("k", "", time.Now()); !ok {
		t.Fatal("the claim was not recorded over the corrupt file")
	}
}

// Codex keeps the same record: a consume whose answer never came is repeated
// by the next redeem, in this process or another.
func TestCodexRedeemRepeatsAnAttemptInDoubt(t *testing.T) {
	fakeCodexHome(t)
	var keys []string
	answers := []func() (json.RawMessage, error){
		func() (json.RawMessage, error) { return nil, &codexRPCError{Code: -32000, Message: "upstream timeout"} },
		func() (json.RawMessage, error) { return json.RawMessage(`{"outcome":"brandNewOutcome"}`), nil },
		func() (json.RawMessage, error) { return json.RawMessage(`{"outcome":"reset"}`), nil },
		func() (json.RawMessage, error) { return json.RawMessage(`{"outcome":"nothingToReset"}`), nil },
	}
	fakeCodexAppServer(t, func(_ string, p any) (json.RawMessage, error) {
		keys = append(keys, p.(map[string]string)["idempotencyKey"])
		return answers[len(keys)-1]()
	})
	credit := usage.ResetCredit{ID: "c1", Status: "available", ExpiresAt: time.Now().Add(time.Hour)}

	_, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background(), credit)
	if err == nil || !strings.Contains(err.Error(), "Trying again is safe") {
		t.Fatalf("err = %v, want the attempt reported as in doubt", err)
	}
	// An outcome this version does not know settles nothing either.
	if res, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background(), credit); err != nil || res.Outcome != "brandNewOutcome" {
		t.Fatalf("outcome = %q (err %v), want the unknown code passed through", res, err)
	}
	if res, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background(), credit); err != nil || res.Outcome != RedeemReset {
		t.Fatalf("outcome = %q (err %v)", res, err)
	}
	if _, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background(), credit); err != nil {
		t.Fatal(err)
	}
	if keys[1] != keys[0] || keys[2] != keys[0] {
		t.Fatalf("keys = %q, want every retry to repeat the attempt in doubt", keys)
	}
	if keys[3] == keys[0] {
		t.Fatalf("keys = %q, want a fresh key once an outcome settled it", keys)
	}
}

func TestCodexConsumeRefusedOverHTTPIsDefinite(t *testing.T) {
	fakeCodexHome(t)
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		return jsonResponse(req, http.StatusForbidden, `{"detail":"forbidden"}`), nil
	})
	_, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background(), usage.ResetCredit{ID: "c1"})
	if err == nil || !strings.Contains(err.Error(), "the redeem request returned HTTP 403") || strings.Contains(err.Error(), "usage endpoint") {
		t.Fatalf("err = %v, want a refused redeem request", err)
	}
	if _, ok := (pendingClaims{provider: "codex"}).lookup(codexCreditKeyBase(usage.ResetCredit{ID: "c1"}), "account-123", time.Now()); ok {
		t.Fatal("a refused request was recorded as in doubt")
	}
}
