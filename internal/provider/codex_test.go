package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

// fakeCodexHome points CODEX_HOME at a temp dir holding credentials, so a test
// never reads — or depends on the presence of — the real ~/.codex/auth.json.
func fakeCodexHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	// The attempts a redemption records live in limitping's config dir.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	authJSON := `{"tokens":{"access_token":"access-token","refresh_token":"refresh-token","account_id":"account-123"}}`
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	// Never reach the real codex binary from a test: it would spend a real
	// reset credit. Tests that exercise the app-server route install their own.
	fakeCodexAppServer(t, func(string, any) (json.RawMessage, error) {
		return nil, fmt.Errorf("%w: disabled in tests", errCodexAppServerUnavailable)
	})
}

func fakeCodexAppServer(t *testing.T, fn func(method string, params any) (json.RawMessage, error)) {
	t.Helper()
	old := codexAppServerCall
	codexAppServerCall = func(_ context.Context, method string, params any) (json.RawMessage, error) {
		return fn(method, params)
	}
	t.Cleanup(func() { codexAppServerCall = old })
}

func TestCodexReadUsageSendsCompatibleHeaders(t *testing.T) {
	oldClient := usageHTTPClient
	defer func() { usageHTTPClient = oldClient }()

	fakeCodexHome(t)

	usageHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := req.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("accept = %q", got)
		}
		if got := req.Header.Get("User-Agent"); got != codexUserAgent {
			t.Fatalf("user-agent = %q", got)
		}
		if got := req.Header.Get("ChatGPT-Account-Id"); got != "account-123" {
			t.Fatalf("account id = %q", got)
		}
		var body string
		switch req.URL.String() {
		case "https://chatgpt.com/backend-api/wham/usage":
			body = `{
				"plan_type": "pro",
				"rate_limit": {
					"limit_reached": false,
					"primary_window": {"used_percent": 12, "limit_window_seconds": 18000, "reset_after_seconds": 12000, "reset_at": 4102444800},
					"secondary_window": {"used_percent": 34, "limit_window_seconds": 604800, "reset_after_seconds": 400000, "reset_at": 4103049600}
				}
			}`
		case "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits":
			if got := req.Header.Get("OpenAI-Beta"); got != "codex-1" {
				t.Fatalf("OpenAI-Beta = %q", got)
			}
			if got := req.Header.Get("originator"); got != "Codex Desktop" {
				t.Fatalf("originator = %q", got)
			}
			body = `{
				"available_count": 1,
				"credits": [
					{
						"status": "available",
						"granted_at": "2026-06-17T17:38:38Z",
						"expires_at": "2026-07-17T17:38:38Z"
					}
				]
			}`
		default:
			t.Fatalf("url = %q", req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}

	u, err := NewCodex(config.ProviderConfig{}).ReadUsage(context.Background())
	if err != nil {
		t.Fatalf("ReadUsage: %v", err)
	}
	if u.Provider != "codex" || u.Plan != "pro" {
		t.Fatalf("usage = %#v", u)
	}
	if u.FiveHour.UsedPercent != 12 || u.Weekly.UsedPercent != 34 {
		t.Fatalf("windows = %#v %#v", u.FiveHour, u.Weekly)
	}
	if u.ResetCredits == nil || u.ResetCredits.AvailableCount != 1 || len(u.ResetCredits.Credits) != 1 {
		t.Fatalf("reset credits = %#v, want one available credit", u.ResetCredits)
	}
	if got := u.ResetCredits.Credits[0].ExpiresAt.Year(); got != 2026 {
		t.Fatalf("reset credit expiry year = %d, want 2026", got)
	}
}

// The backend reports an idle limit as a full-length window that slides forward
// on every read, not as "no window". Observed 2026-09-19 on an idle 5h window:
// used_percent 0, reset_after_seconds 18000 == limit_window_seconds, reset_at
// moving with the clock between reads. Reading that as a running window parked
// watch for good — it kept seeing 5h left and never pinged — so it has to come
// out of the reader as a window with no reset time.
func TestCodexWindowAnchoring(t *testing.T) {
	now := time.Date(2026, 9, 19, 18, 29, 44, 0, time.UTC)
	fiveHourAt := func(secs int) int64 { return now.Add(time.Duration(secs) * time.Second).Unix() }

	cases := []struct {
		name       string
		w          codexWindow
		wantActive bool
	}{
		{
			"idle 5h window slides forward",
			codexWindow{UsedPercent: 0, LimitWindowSeconds: 18000, ResetAfterSeconds: 18000, ResetAt: fiveHourAt(18000)},
			false,
		},
		{
			// A window only a ping has touched: 0% used, but running.
			"window a ping just started",
			codexWindow{UsedPercent: 0, LimitWindowSeconds: 18000, ResetAfterSeconds: 17975, ResetAt: fiveHourAt(17975)},
			true,
		},
		{
			"window with real consumption",
			codexWindow{UsedPercent: 58, LimitWindowSeconds: 18000, ResetAfterSeconds: 9000, ResetAt: fiveHourAt(9000)},
			true,
		},
		{
			"expired window not yet rolled over",
			codexWindow{UsedPercent: 100, LimitWindowSeconds: 18000, ResetAfterSeconds: 0, ResetAt: fiveHourAt(-12)},
			false, // reset time is in the past, so nothing is running
		},
		{
			// reset_after_seconds is the skew-free signal; without it the local
			// clock decides, and a full window ahead still reads as idle.
			"idle window without reset_after_seconds",
			codexWindow{UsedPercent: 0, LimitWindowSeconds: 18000, ResetAt: fiveHourAt(18000)},
			false,
		},
		{
			"anchored window without reset_after_seconds",
			codexWindow{UsedPercent: 0, LimitWindowSeconds: 18000, ResetAt: fiveHourAt(17000)},
			true,
		},
	}
	for _, c := range cases {
		got := codexWindowToUsage(c.w, now)
		if active := !got.ResetsAt.IsZero() && now.Before(got.ResetsAt); active != c.wantActive {
			t.Errorf("%s: running = %t, want %t (resets_at %v)", c.name, active, c.wantActive, got.ResetsAt)
		}
		// Whatever the verdict, the limit is still enforced: the window must not
		// collapse into usage.Window.Missing, which means "no such limit".
		if got.Missing() {
			t.Errorf("%s: window reported as missing; the limit is enforced", c.name)
		}
		if got.UsedPercent != c.w.UsedPercent || got.WindowSeconds != c.w.LimitWindowSeconds {
			t.Errorf("%s: window = %#v, want used/length preserved", c.name, got)
		}
	}
}

// An idle weekly window slides the same way, and must not be read as running
// either — otherwise the weekly-only regime waits for a reset that never comes.
func TestCodexWindowAnchoringWeekly(t *testing.T) {
	now := time.Date(2026, 9, 19, 18, 29, 44, 0, time.UTC)
	idle := codexWindow{
		UsedPercent:        0,
		LimitWindowSeconds: 604800,
		ResetAfterSeconds:  604800,
		ResetAt:            now.Add(604800 * time.Second).Unix(),
	}
	if got := codexWindowToUsage(idle, now); !got.ResetsAt.IsZero() {
		t.Fatalf("idle weekly window resets_at = %v, want none", got.ResetsAt)
	}
}

// Since 2026-07-12 (5h limit temporarily removed) the weekly window arrives in
// primary_window with secondary_window null; windows must be classified by
// length, not position, and the missing 5h window must stay missing.
func TestCodexReadUsageWeeklyOnlyRegime(t *testing.T) {
	oldClient := usageHTTPClient
	defer func() { usageHTTPClient = oldClient }()

	fakeCodexHome(t)

	usageHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits" {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
				Request:    req,
			}, nil
		}
		body := `{
			"plan_type": "plus",
			"rate_limit": {
				"allowed": true,
				"limit_reached": false,
				"primary_window": {"used_percent": 24, "limit_window_seconds": 604800, "reset_after_seconds": 400000, "reset_at": 4103049600},
				"secondary_window": null
			},
			"rate_limit_reset_credits": {"available_count": 3}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}

	u, err := NewCodex(config.ProviderConfig{}).ReadUsage(context.Background())
	if err != nil {
		t.Fatalf("ReadUsage: %v", err)
	}
	if !u.FiveHour.Missing() {
		t.Fatalf("five hour = %#v, want missing (limit not enforced)", u.FiveHour)
	}
	if u.Weekly.UsedPercent != 24 || u.Weekly.WindowSeconds != 604800 {
		t.Fatalf("weekly = %#v, want the primary window classified as weekly", u.Weekly)
	}
	if u.ResetCredits == nil || u.ResetCredits.AvailableCount != 3 {
		t.Fatalf("reset credits = %#v, want inline count 3 after detail endpoint failure", u.ResetCredits)
	}
}

func TestCodexReadUsageIgnoresResetCreditFailure(t *testing.T) {
	oldClient := usageHTTPClient
	defer func() { usageHTTPClient = oldClient }()

	fakeCodexHome(t)

	usageHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits" {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
				Request:    req,
			}, nil
		}
		body := `{
			"plan_type": "pro",
			"rate_limit": {
				"limit_reached": false,
				"primary_window": {"used_percent": 12, "limit_window_seconds": 18000, "reset_after_seconds": 12000, "reset_at": 4102444800},
				"secondary_window": {"used_percent": 34, "limit_window_seconds": 604800, "reset_after_seconds": 400000, "reset_at": 4103049600}
			}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}

	u, err := NewCodex(config.ProviderConfig{}).ReadUsage(context.Background())
	if err != nil {
		t.Fatalf("ReadUsage: %v", err)
	}
	if u.ResetCredits != nil {
		t.Fatalf("reset credits = %#v, want nil after reset endpoint failure", u.ResetCredits)
	}
}

func TestCodexUsageURLFromBase(t *testing.T) {
	cases := map[string]string{
		"":                                 "https://chatgpt.com/backend-api/wham/usage",
		"https://chatgpt.com/backend-api/": "https://chatgpt.com/backend-api/wham/usage",
		"https://chat.openai.com":          "https://chat.openai.com/backend-api/wham/usage",
		"https://api.openai.com":           "https://api.openai.com/api/codex/usage",
		"https://example.test/custom/base": "https://example.test/custom/base/api/codex/usage",
		"://bad":                           "https://chatgpt.com/backend-api/wham/usage",
	}
	for base, want := range cases {
		if got := codexUsageURLFromBase(base); got != want {
			t.Fatalf("codexUsageURLFromBase(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestCodexResetCreditsURLFromBase(t *testing.T) {
	cases := map[string]string{
		"":                                 "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits",
		"https://chatgpt.com/backend-api/": "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits",
		"https://chat.openai.com":          "https://chat.openai.com/backend-api/wham/rate-limit-reset-credits",
		"https://api.openai.com":           "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits",
		"://bad":                           "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits",
	}
	for base, want := range cases {
		if got := codexResetCreditsURLFromBase(base); got != want {
			t.Fatalf("codexResetCreditsURLFromBase(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestParseCodexBaseURL(t *testing.T) {
	contents := `
model = "gpt-5.6-luna"
chatgpt_base_url = "https://api.openai.com"
`
	if got := parseCodexBaseURL(contents); got != "https://api.openai.com" {
		t.Fatalf("base url = %q", got)
	}
}

func TestCodexTriggerDryRunUsesEphemeralExecCommand(t *testing.T) {
	c := NewCodex(config.ProviderConfig{
		Prompt:          "ok",
		Model:           "gpt-5.6-luna",
		ReasoningEffort: "low",
		ExtraArgs: []string{
			"--no-alt-screen",
			"--search",
			"--sandbox", "danger-full-access",
		},
	})

	res, err := c.Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	want := "codex exec --ephemeral --json --skip-git-repo-check --disable hooks --sandbox read-only " +
		"-c model_reasoning_effort=low -m gpt-5.6-luna --search --sandbox danger-full-access ok"
	if res.Command != want {
		t.Fatalf("command = %q, want %q", res.Command, want)
	}
}

// --ephemeral is the whole point of the headless path: without it every ping
// leaves an "ok" conversation in `codex resume` and the Codex Desktop thread
// list, and the interactive CLI has no equivalent flag at all.
func TestCodexTriggerNeverPersistsThePingSession(t *testing.T) {
	fakeCodexHome(t)
	res, err := NewCodex(config.ProviderConfig{Prompt: "ok"}).Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	for _, want := range []string{"exec", "--ephemeral", "--json"} {
		if !strings.Contains(res.Command, want) {
			t.Fatalf("command = %q, want it to carry %q", res.Command, want)
		}
	}
}

func TestCodexExecArgsDropsInteractiveOnlyFlags(t *testing.T) {
	got := codexExecArgs([]string{
		"--no-alt-screen",
		"--remote", "ws://localhost:1234",
		"--remote-auth-token-env=TOKEN",
		"--search",
		"-C", "/tmp/project",
	})
	want := []string{"--search", "-C", "/tmp/project"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exec args = %#v, want %#v", got, want)
	}
}

// The failure this path must never hide: the CLI exits 0 but no turn ran, so
// nothing was billed and no window started. Reporting that as a successful
// ping is what makes watch wait out a window that never began.
func TestCodexExecUsageRequiresACompletedTurn(t *testing.T) {
	var res TriggerResult
	if _, completed := codexExecUsage([]byte(`{"type":"thread.started","thread_id":"t"}
{"type":"turn.started"}
`), &res); completed {
		t.Fatal("a stream without turn.completed must not read as a dispatched request")
	}
	if res.HasUsage {
		t.Fatalf("usage = %+v, want none reported", res)
	}

	cached, completed := codexExecUsage([]byte(`{"type":"thread.started","thread_id":"t"}
{"type":"turn.completed","usage":{"input_tokens":19544,"cached_input_tokens":8960,"output_tokens":15,"reasoning_output_tokens":0}}
`), &res)
	if !completed || !res.HasUsage {
		t.Fatalf("completed=%t usage=%+v, want a dispatched request", completed, res)
	}
	if res.InputTokens != 19544 || res.OutputTokens != 15 || res.TotalTokens != 19559 || cached != 8960 {
		t.Fatalf("usage = %+v (cached %d), want 19544 in / 15 out / 8960 cached", res, cached)
	}
}

func TestCodexRedeemResetCreditReportsOutcome(t *testing.T) {
	fakeCodexHome(t)
	var body []byte
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != codexConsumeURL() {
			t.Fatalf("consume request = %s %s", req.Method, req.URL)
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := req.Header.Get("ChatGPT-Account-Id"); got != "account-123" {
			t.Fatalf("ChatGPT-Account-Id = %q", got)
		}
		var err error
		if body, err = io.ReadAll(req.Body); err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":"reset","windows_reset":["primary"]}`)),
			Request:    req,
		}, nil
	})

	got, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background(), usage.ResetCredit{})
	if err != nil {
		t.Fatalf("RedeemResetCredit: %v", err)
	}
	if got.Outcome != RedeemReset {
		t.Fatalf("outcome = %q, want %q", got, RedeemReset)
	}
	var sent map[string]string
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, body)
	}
	if sent["redeem_request_id"] == "" {
		t.Fatalf("request body = %s, want an idempotency key", body)
	}
}

func TestCodexRedeemResetCreditNormalizesCamelCaseOutcomes(t *testing.T) {
	fakeCodexHome(t)
	cases := map[string]string{
		"nothingToReset":   RedeemNothingToReset,
		"nothing_to_reset": RedeemNothingToReset,
		"noCredit":         RedeemNoCredit,
		"alreadyRedeemed":  RedeemAlreadyRedeemed,
		"brand_new_code":   "brand_new_code",
	}
	for code, want := range cases {
		t.Run(code, func(t *testing.T) {
			useTransport(t, func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"code":"` + code + `"}`)),
					Request:    req,
				}, nil
			})
			got, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background(), usage.ResetCredit{})
			if err != nil || got.Outcome != want {
				t.Fatalf("outcome = %q (err %v), want %q", got, err, want)
			}
		})
	}
}

func TestCodexRedeemResetCreditRejectsOutcomelessResponse(t *testing.T) {
	fakeCodexHome(t)
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    req,
		}, nil
	})
	if _, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background(), usage.ResetCredit{}); err == nil {
		t.Fatal("a response without an outcome must not be reported as a redemption")
	}
}

func TestCodexAutoRedeemSkipsUntilExpiryAndThenThrottles(t *testing.T) {
	fakeCodexHome(t)
	requests := 0
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":"nothing_to_reset"}`)),
			Request:    req,
		}, nil
	})
	c := NewCodex(config.ProviderConfig{})

	fresh := &usage.Usage{ResetCredits: &usage.ResetCredits{Credits: []usage.ResetCredit{
		{Status: "available", ExpiresAt: time.Now().Add(10 * 24 * time.Hour)},
	}}}
	if outcome, err := c.AutoRedeemResetCredit(context.Background(), fresh); outcome.Outcome != "" || err != nil {
		t.Fatalf("outcome = %q (err %v), want no attempt for a credit with 10 days left", outcome, err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}

	expiring := &usage.Usage{ResetCredits: &usage.ResetCredits{Credits: []usage.ResetCredit{
		{Status: "available", ExpiresAt: time.Now().Add(30 * time.Minute)},
	}}}
	if outcome, err := c.AutoRedeemResetCredit(context.Background(), expiring); outcome.Outcome != RedeemNothingToReset || err != nil {
		t.Fatalf("outcome = %q (err %v), want %q", outcome, err, RedeemNothingToReset)
	}
	// A 1-minute poll loop must not retry the refused redemption every cycle.
	if outcome, err := c.AutoRedeemResetCredit(context.Background(), expiring); outcome.Outcome != "" || err != nil {
		t.Fatalf("outcome = %q (err %v), want the cooldown to suppress the retry", outcome, err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestCreditIdempotencyKeyIsStablePerCredit(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	first := creditIdempotencyKey(usage.ResetCredit{ExpiresAt: expires})
	// Same credit read again (in another zone) must reuse the key, so a retry
	// after a lost response cannot spend a second credit.
	if second := creditIdempotencyKey(usage.ResetCredit{ExpiresAt: expires.In(time.UTC)}); second != first {
		t.Fatalf("key = %q then %q, want them equal", first, second)
	}
	if other := creditIdempotencyKey(usage.ResetCredit{ExpiresAt: expires.Add(time.Minute)}); other == first {
		t.Fatal("a different credit reused the same idempotency key")
	}
	if randomIdempotencyKey() == randomIdempotencyKey() {
		t.Fatal("manual redemptions must not share an idempotency key")
	}
}

func TestCodexConsumeURLFromBase(t *testing.T) {
	cases := map[string]string{
		"":                                    codexDefaultBaseURL + codexConsumePath,
		"https://chatgpt.com/backend-api":     "https://chatgpt.com/backend-api" + codexConsumePath,
		"https://proxy.internal/backend-api/": "https://proxy.internal/backend-api" + codexConsumePath,
		"https://api.openai.com/v1":           codexDefaultBaseURL + codexConsumePath,
	}
	for base, want := range cases {
		if got := codexResetURLFromBase(base, codexConsumePath); got != want {
			t.Fatalf("codexResetURLFromBase(%q) = %q, want %q", base, got, want)
		}
	}
}

// catalogEntry mirrors the fields limitping reads out of the Codex CLI's
// models_cache.json.
type catalogEntry struct {
	slug        string
	visibility  string
	priority    int
	description string
}

func writeCodexModelsCache(t *testing.T, models ...catalogEntry) {
	t.Helper()
	entries := make([]map[string]any, 0, len(models))
	for _, m := range models {
		entries = append(entries, map[string]any{
			"slug":        m.slug,
			"visibility":  m.visibility,
			"priority":    m.priority,
			"description": m.description,
		})
	}
	data, err := json.Marshal(map[string]any{"models": entries})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("CODEX_HOME"), "models_cache.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodexTriggerRejectsRetiredModel(t *testing.T) {
	fakeCodexHome(t)
	writeCodexModelsCache(t,
		catalogEntry{slug: "gpt-6-astra", visibility: "list", priority: 1},
		catalogEntry{slug: "gpt-5.6-luna", visibility: "list", priority: 8},
		catalogEntry{slug: "codex-auto-review", visibility: "hide", priority: 43},
	)

	c := NewCodex(config.ProviderConfig{Prompt: "ok", Model: "gpt-5.4-mini"})
	_, err := c.Trigger(context.Background(), true)
	if err == nil {
		t.Fatal("dry-run trigger succeeded, want a retired-model error")
	}
	for _, want := range []string{`"gpt-5.4-mini"`, "gpt-6-astra", "gpt-5.6-luna", "models_cache.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	// Internal models are valid but not worth suggesting.
	if strings.Contains(err.Error(), "codex-auto-review") {
		t.Errorf("error suggests a hidden model: %q", err)
	}
}

func TestCodexTriggerAcceptsCataloguedAndUnsetModels(t *testing.T) {
	fakeCodexHome(t)
	writeCodexModelsCache(t,
		catalogEntry{slug: "gpt-5.6-luna", visibility: "list", priority: 8},
		catalogEntry{slug: "codex-auto-review", visibility: "hide", priority: 43},
	)

	for _, model := range []string{"gpt-5.6-luna", "codex-auto-review", ""} {
		c := NewCodex(config.ProviderConfig{Prompt: "ok", Model: model})
		if _, err := c.Trigger(context.Background(), true); err != nil {
			t.Errorf("model %q rejected: %v", model, err)
		}
	}
}

// The catalog is a private Codex file. If it is missing or unreadable the ping
// must still go out — a stale model is a likelier failure than no cache at all,
// but guessing wrong here would block pings that would have worked.
func TestCodexTriggerSkipsModelCheckWithoutCatalog(t *testing.T) {
	fakeCodexHome(t)

	c := NewCodex(config.ProviderConfig{Prompt: "ok", Model: "anything-at-all"})
	if _, err := c.Trigger(context.Background(), true); err != nil {
		t.Fatalf("trigger without a models cache: %v", err)
	}

	if err := os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "models_cache.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Trigger(context.Background(), true); err != nil {
		t.Fatalf("trigger with an unparseable models cache: %v", err)
	}
}

// With no model configured the ping must land on the cheapest catalogued model,
// not on the working model the user set in the Codex CLI — a ping only needs to
// be billable, and a working model is typically a much pricier tier.
func TestCodexUnsetModelPicksTheCheapestOverTheCLIWorkingModel(t *testing.T) {
	fakeCodexHome(t)
	writeCodexModelsCache(t,
		catalogEntry{slug: "gpt-6-astra", visibility: "list", priority: 1,
			description: "Our most capable model for complex, demanding work."},
		catalogEntry{slug: "gpt-5.6-sol", visibility: "list", priority: 6,
			description: "Reliable agentic workhorse for everyday tasks."},
		catalogEntry{slug: "gpt-5.6-luna", visibility: "list", priority: 8,
			description: "Fast and affordable agentic coding model."},
		catalogEntry{slug: "gpt-5.5", visibility: "list", priority: 12,
			description: "Proven previous-generation model for coding and general work."},
		// Hidden but also budget-tier: never chosen on the user's behalf.
		catalogEntry{slug: "gpt-reserve", visibility: "hide", priority: 3,
			description: "Fast and affordable agentic coding model."},
	)
	writeCodexCLIConfig(t, `model = "gpt-5.6-sol"`)

	res, err := NewCodex(config.ProviderConfig{Prompt: "ok"}).Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	if res.Model != "gpt-5.6-luna" {
		t.Fatalf("Model = %q, want the cheapest catalogued model", res.Model)
	}
	if !strings.Contains(res.Command, "-m gpt-5.6-luna") {
		t.Fatalf("command = %q, want the cheapest model pinned with -m", res.Command)
	}
}

// Among several budget-tier entries, the one Codex itself ranks furthest from
// its flagship wins, so the choice is deterministic rather than catalog-order
// dependent.
func TestCodexCheapestModelPrefersTheLowestRanked(t *testing.T) {
	fakeCodexHome(t)
	writeCodexModelsCache(t,
		catalogEntry{slug: "budget-new", visibility: "list", priority: 4, description: "Fast and affordable."},
		catalogEntry{slug: "budget-old", visibility: "list", priority: 9, description: "Fast and affordable."},
		catalogEntry{slug: "flagship", visibility: "list", priority: 1, description: "Our most capable model."},
	)
	if got := codexCheapestModel(); got != "budget-old" {
		t.Fatalf("codexCheapestModel() = %q, want budget-old", got)
	}
}

// A "mini"-style slug is the other naming OpenAI has used for budget variants.
func TestCodexCheapestModelRecognizesMiniNaming(t *testing.T) {
	fakeCodexHome(t)
	writeCodexModelsCache(t,
		catalogEntry{slug: "gpt-9", visibility: "list", priority: 1, description: "Our most capable model."},
		catalogEntry{slug: "gpt-9-mini", visibility: "list", priority: 5, description: "Smaller variant."},
	)
	if got := codexCheapestModel(); got != "gpt-9-mini" {
		t.Fatalf("codexCheapestModel() = %q, want gpt-9-mini", got)
	}
}

// Nothing recognizably budget-tier: fall back to letting the CLI choose, and
// report what that will be rather than guessing a model on stale price wording.
func TestCodexUnsetModelFallsBackToTheCLIWhenNoBudgetTierExists(t *testing.T) {
	fakeCodexHome(t)
	writeCodexModelsCache(t,
		catalogEntry{slug: "gpt-6-astra", visibility: "list", priority: 1,
			description: "Our most capable model for complex, demanding work."},
	)
	writeCodexCLIConfig(t, `model = "gpt-6-astra"`)

	res, err := NewCodex(config.ProviderConfig{Prompt: "ok"}).Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	if strings.Contains(res.Command, "-m ") {
		t.Fatalf("command = %q, want no -m when no budget tier is identifiable", res.Command)
	}
	if res.Model != "gpt-6-astra" {
		t.Fatalf("Model = %q, want the CLI's own configured model reported", res.Model)
	}
}

// An explicit limitping model always wins over the catalog pick.
func TestCodexConfiguredModelWinsOverTheCheapest(t *testing.T) {
	fakeCodexHome(t)
	writeCodexModelsCache(t,
		catalogEntry{slug: "gpt-5.6-luna", visibility: "list", priority: 8, description: "Fast and affordable."},
		catalogEntry{slug: "gpt-5.5", visibility: "list", priority: 12, description: "Previous generation."},
	)

	res, err := NewCodex(config.ProviderConfig{Prompt: "ok", Model: "gpt-5.5"}).Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	if res.Model != "gpt-5.5" || !strings.Contains(res.Command, "-m gpt-5.5") {
		t.Fatalf("Model = %q, command = %q, want the configured model", res.Model, res.Command)
	}
}

func writeCodexCLIConfig(t *testing.T, contents string) {
	t.Helper()
	path := filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")
	if err := os.WriteFile(path, []byte(contents+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A ping is a synthetic session: the user's hooks must not fire for it (which
// would also mark it as an active Codex session), and nothing reviews what the
// model does on this path, so it must not inherit a permissive sandbox from the
// user's Codex config.
func TestCodexTriggerRunsTheSyntheticSessionWithoutHooksOrWriteAccess(t *testing.T) {
	fakeCodexHome(t)
	res, err := NewCodex(config.ProviderConfig{Prompt: "ok"}).Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	for _, want := range []string{"--disable hooks", "--sandbox read-only"} {
		if !strings.Contains(res.Command, want) {
			t.Fatalf("command = %q, want it to carry %q", res.Command, want)
		}
	}
}
