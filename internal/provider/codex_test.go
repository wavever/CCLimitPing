package provider

import (
	"context"
	"encoding/json"
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
	authJSON := `{"tokens":{"access_token":"access-token","refresh_token":"refresh-token","account_id":"account-123"}}`
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatal(err)
	}
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
					"primary_window": {"used_percent": 12, "limit_window_seconds": 18000, "reset_at": 4102444800},
					"secondary_window": {"used_percent": 34, "limit_window_seconds": 604800, "reset_at": 4103049600}
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
				"primary_window": {"used_percent": 24, "limit_window_seconds": 604800, "reset_at": 4103049600},
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

func TestSparkReadUsageReportsSparkProvider(t *testing.T) {
	oldClient := usageHTTPClient
	defer func() { usageHTTPClient = oldClient }()

	fakeCodexHome(t)

	usageHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{
			"plan_type": "plus",
			"rate_limit": {
				"limit_reached": false,
				"primary_window": {"used_percent": 5, "limit_window_seconds": 18000, "reset_at": 4102444800},
				"secondary_window": {"used_percent": 7, "limit_window_seconds": 604800, "reset_at": 4103049600}
			},
			"additional_rate_limits": [
				{
					"limit_name": "GPT-5.3-Codex-Spark",
					"metered_feature": "codex_bengalfox",
					"rate_limit": {
						"limit_reached": false,
						"primary_window": {"used_percent": 1, "limit_window_seconds": 18000, "reset_at": 4102444900},
						"secondary_window": {"used_percent": 2, "limit_window_seconds": 604800, "reset_at": 4103049700}
					}
				}
			]
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}

	u, err := NewSpark(config.ProviderConfig{}).ReadUsage(context.Background())
	if err != nil {
		t.Fatalf("ReadUsage: %v", err)
	}
	if u.Provider != "spark" || u.Plan != "plus" {
		t.Fatalf("usage = %#v, want spark/plus", u)
	}
	if u.FiveHour.UsedPercent != 1 || u.Weekly.UsedPercent != 2 {
		t.Fatalf("windows = %#v %#v", u.FiveHour, u.Weekly)
	}
}

func TestSparkReadUsageRequiresSparkLimit(t *testing.T) {
	oldClient := usageHTTPClient
	defer func() { usageHTTPClient = oldClient }()

	fakeCodexHome(t)

	usageHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{
			"plan_type": "plus",
			"rate_limit": {
				"limit_reached": false,
				"primary_window": {"used_percent": 5, "limit_window_seconds": 18000, "reset_at": 4102444800},
				"secondary_window": {"used_percent": 7, "limit_window_seconds": 604800, "reset_at": 4103049600}
			}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}

	_, err := NewSpark(config.ProviderConfig{}).ReadUsage(context.Background())
	if err == nil || !strings.Contains(err.Error(), `model "gpt-5.3-codex-spark"`) {
		t.Fatalf("ReadUsage error = %v, want missing spark limit", err)
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
				"primary_window": {"used_percent": 12, "limit_window_seconds": 18000, "reset_at": 4102444800},
				"secondary_window": {"used_percent": 34, "limit_window_seconds": 604800, "reset_at": 4103049600}
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
model = "gpt-5.4-mini"
chatgpt_base_url = "https://api.openai.com"
`
	if got := parseCodexBaseURL(contents); got != "https://api.openai.com" {
		t.Fatalf("base url = %q", got)
	}
}

func TestCodexTriggerDryRunUsesInteractiveCommand(t *testing.T) {
	c := NewCodex(config.ProviderConfig{
		Prompt:          "ok",
		Model:           "gpt-5.4-mini",
		ReasoningEffort: "low",
		ExtraArgs: []string{
			"--skip-git-repo-check",
			"--json",
			"--output-schema", "schema.json",
			"--search",
			"--sandbox", "read-only",
		},
	})

	res, err := c.Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	want := "codex -c model_reasoning_effort=low -m gpt-5.4-mini --search --sandbox read-only -c tui.notifications=[\"agent-turn-complete\"] -c tui.notification_method=\"osc9\" -c tui.notification_condition=\"always\" ok"
	if res.Command != want {
		t.Fatalf("command = %q, want %q", res.Command, want)
	}
	if strings.Contains(res.Command, "exec") || strings.Contains(res.Command, "--json") {
		t.Fatalf("command still uses headless mode: %q", res.Command)
	}
}

func TestCodexTriggerWaitsForTurnCompleteNotification(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	turnPath := filepath.Join(dir, "turn")
	termPath := filepath.Join(dir, "term")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$CODEX_TEST_ARGS"
printf '%s' "$TERM" > "$CODEX_TEST_TERM"
printf 'startup screen\n'
sleep 0.08
printf 'submitted' > "$CODEX_TEST_TURN"
i=0
while [ "$i" -lt 20 ]; do
  printf '.'
  sleep 0.01
  i=$((i + 1))
done
printf '\033]9;turn finished\007'
trap 'exit 0' INT TERM
while :; do
  printf '.'
  sleep 0.01
done
`
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_TEST_ARGS", argsPath)
	t.Setenv("CODEX_TEST_TURN", turnPath)
	t.Setenv("CODEX_TEST_TERM", termPath)
	t.Setenv("TERM", "dumb")

	timing := codexInteractiveTiming{
		maxWait:   time.Second,
		exitGrace: 50 * time.Millisecond,
	}
	started := time.Now()
	_, err := triggerCodexWithTiming(context.Background(), config.ProviderConfig{
		Prompt: "ping through pty",
		Model:  "test-model",
	}, false, timing)
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("trigger took %s, want completion marker to stop it before fallback", elapsed)
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "ping through pty") {
		t.Fatalf("arguments = %q, want positional prompt", args)
	}
	turn, err := os.ReadFile(turnPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(turn) != "submitted" {
		t.Fatalf("turn marker = %q, want submitted", turn)
	}
	term, err := os.ReadFile(termPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(term) != "xterm-256color" {
		t.Fatalf("TERM = %q, want xterm-256color", term)
	}
}

func TestSparkTriggerDryRunUsesSparkModel(t *testing.T) {
	c := NewSpark(config.ProviderConfig{
		Prompt:          "ok",
		Model:           "gpt-5.3-codex-spark",
		ReasoningEffort: "low",
	})

	if c.Name() != "spark" {
		t.Fatalf("Name() = %q, want spark", c.Name())
	}
	res, err := c.Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	want := "codex -c model_reasoning_effort=low -m gpt-5.3-codex-spark -c tui.notifications=[\"agent-turn-complete\"] -c tui.notification_method=\"osc9\" -c tui.notification_condition=\"always\" ok"
	if res.Command != want {
		t.Fatalf("command = %q, want %q", res.Command, want)
	}
}

func TestCodexInteractiveArgsDropsExecOnlyFlags(t *testing.T) {
	got := codexInteractiveArgs([]string{
		"--skip-git-repo-check",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--json",
		"--output-schema=schema.json",
		"--output-last-message", "out.txt",
		"--color", "never",
		"--search",
		"-C", "/tmp/project",
	})
	want := []string{"--search", "-C", "/tmp/project"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interactive args = %#v, want %#v", got, want)
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

	got, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background())
	if err != nil {
		t.Fatalf("RedeemResetCredit: %v", err)
	}
	if got != RedeemReset {
		t.Fatalf("outcome = %q, want %q", got, RedeemReset)
	}
	var sent map[string]string
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("request body is not JSON: %v (%s)", err, body)
	}
	if sent["idempotency_key"] == "" {
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
			got, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background())
			if err != nil || got != want {
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
	if _, err := NewCodex(config.ProviderConfig{}).RedeemResetCredit(context.Background()); err == nil {
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
	if outcome, err := c.AutoRedeemResetCredit(context.Background(), fresh); outcome != "" || err != nil {
		t.Fatalf("outcome = %q (err %v), want no attempt for a credit with 10 days left", outcome, err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}

	expiring := &usage.Usage{ResetCredits: &usage.ResetCredits{Credits: []usage.ResetCredit{
		{Status: "available", ExpiresAt: time.Now().Add(30 * time.Minute)},
	}}}
	if outcome, err := c.AutoRedeemResetCredit(context.Background(), expiring); outcome != RedeemNothingToReset || err != nil {
		t.Fatalf("outcome = %q (err %v), want %q", outcome, err, RedeemNothingToReset)
	}
	// A 1-minute poll loop must not retry the refused redemption every cycle.
	if outcome, err := c.AutoRedeemResetCredit(context.Background(), expiring); outcome != "" || err != nil {
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
