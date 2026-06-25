package provider

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wavever/CCLimitPing/internal/config"
)

func TestCodexReadUsageSendsCompatibleHeaders(t *testing.T) {
	oldClient := usageHTTPClient
	defer func() { usageHTTPClient = oldClient }()

	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	authJSON := `{"tokens":{"access_token":"access-token","refresh_token":"refresh-token","account_id":"account-123"}}`
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	usageHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.String(); got != "https://chatgpt.com/backend-api/wham/usage" {
			t.Fatalf("url = %q", got)
		}
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
	if u.Provider != "codex" || u.Plan != "pro" {
		t.Fatalf("usage = %#v", u)
	}
	if u.FiveHour.UsedPercent != 12 || u.Weekly.UsedPercent != 34 {
		t.Fatalf("windows = %#v %#v", u.FiveHour, u.Weekly)
	}
}

func TestSparkReadUsageReportsSparkProvider(t *testing.T) {
	oldClient := usageHTTPClient
	defer func() { usageHTTPClient = oldClient }()

	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	authJSON := `{"tokens":{"access_token":"access-token","refresh_token":"refresh-token","account_id":"account-123"}}`
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatal(err)
	}

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

	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	authJSON := `{"tokens":{"access_token":"access-token","refresh_token":"refresh-token","account_id":"account-123"}}`
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(authJSON), 0o600); err != nil {
		t.Fatal(err)
	}

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
	want := "codex -c model_reasoning_effort=low -m gpt-5.4-mini --search --sandbox read-only ok"
	if res.Command != want {
		t.Fatalf("command = %q, want %q", res.Command, want)
	}
	if strings.Contains(res.Command, "exec") || strings.Contains(res.Command, "--json") {
		t.Fatalf("command still uses headless mode: %q", res.Command)
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
	want := "codex -c model_reasoning_effort=low -m gpt-5.3-codex-spark ok"
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
