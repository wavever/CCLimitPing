package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
)

func TestClaudeInteractiveArgsDropsPrintOnlyFlags(t *testing.T) {
	got := claudeInteractiveArgs([]string{
		"--max-turns", "1",
		"--output-format=json",
		"--tools", "Read",
		"--bare",
		"--permission-mode", "plan",
		"--json-schema", "{}",
	})
	want := []string{"--tools", "Read", "--permission-mode", "plan"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interactive args = %#v, want %#v", got, want)
	}
}

func fakeClaudeHelp(t *testing.T, text string) {
	t.Helper()
	old := claudeHelp
	claudeHelp = func() string { return text }
	t.Cleanup(func() { claudeHelp = old })
}

const claudeHelpWithIsolationFlags = `Options:
  --session-id <uuid>                   Use a specific session ID
  --setting-sources <sources>           Comma-separated list of setting sources
  --settings <file-or-json>             Path to a settings JSON file
  --strict-mcp-config                   Only use MCP servers from --mcp-config
  --tools <tools...>                    Specify the list of available tools
`

func TestClaudeTriggerIsolatesThePing(t *testing.T) {
	fakeClaudeHelp(t, claudeHelpWithIsolationFlags)
	c := NewClaude(config.ProviderConfig{Prompt: ".", Model: "haiku"})
	res, err := c.Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	re := regexp.MustCompile(`^claude --model haiku --session-id [0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12} ` +
		`--settings "{\\"disableAllHooks\\":true,\\"promptSuggestionEnabled\\":false}" --strict-mcp-config --tools "" -- \.$`)
	if !re.MatchString(res.Command) {
		t.Fatalf("command = %q, want the isolating flags before the prompt", res.Command)
	}
}

func TestClaudeFlagSupportedIgnoresMentionsInDescriptions(t *testing.T) {
	fakeClaudeHelp(t, `Options:
  --bare                                Minimal mode: Explicitly provide
                                        context via --system-prompt,
                                        --settings, --agents, --plugin-dir.
  --allowedTools, --allowed-tools <tools...>
      Comma or space-separated list; see also --tools
  -c, --continue                        Continue the most recent conversation
`)
	for flag, want := range map[string]bool{
		"--settings":      false, // only in --bare's wrapped description
		"--tools":         false, // only in prose
		"--allowed-tools": true,  // declared as an alias
		"--continue":      true,  // after a short alias
		"--bare":          true,
	} {
		if got := claudeFlagSupported(flag); got != want {
			t.Errorf("claudeFlagSupported(%s) = %t, want %t", flag, got, want)
		}
	}
}

// --tools and --allowedTools take any number of values, so a prompt that
// follows them unprotected is parsed as one more tool and never sent — the
// session then exits cleanly having started no window.
func TestClaudeTriggerEndsOptionsBeforeThePrompt(t *testing.T) {
	fakeClaudeHelp(t, claudeHelpWithIsolationFlags)
	c := NewClaude(config.ProviderConfig{Prompt: "ping", ExtraArgs: []string{"--allowedTools", "Read", "Edit"}})
	res, err := c.Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	if !strings.HasSuffix(res.Command, " -- ping") {
		t.Fatalf("command = %q, want the prompt after a closing --", res.Command)
	}
}

func TestClaudeTrustPromptIsAnError(t *testing.T) {
	// As the TUI paints it: cursor moves for spaces, styling around words.
	screen := "\x1b[1mQuick\x1b[1Csafety\x1b[1Ccheck:\x1b[0m Is\x1b[1Cthis\x1b[1Ca\x1b[1Cproject\x1b[1Cyou\x1b[1Ccreated\x1b[1Cor\x1b[1Cone\x1b[1Cyou\x1b[1Ctrust?\r\n" +
		"\x1b[36m❯\x1b[39m No, exit\r\n  Yes,\x1b[1CI\x1b[1Ctrust\x1b[1Cthis\x1b[1Cfolder\r\n"
	if err := claudeOutputError([]byte(screen)); err == nil {
		t.Fatal("a ping that stopped at the trust dialog sent nothing and must not count as a success")
	}
	if err := claudeOutputError([]byte("⏺ Hi! What can I help you with?\r\n")); err != nil {
		t.Fatalf("an ordinary reply is not an error: %v", err)
	}
}

func TestClaudeTriggerLeavesUserFlagsAlone(t *testing.T) {
	fakeClaudeHelp(t, claudeHelpWithIsolationFlags)
	c := NewClaude(config.ProviderConfig{Prompt: ".", ExtraArgs: []string{"--settings=/etc/ping.json", "--tools", "Read"}})
	res, err := c.Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	if strings.Count(res.Command, "--settings") != 1 || strings.Count(res.Command, "--tools") != 1 {
		t.Fatalf("command = %q: a flag the user sets must not be passed twice", res.Command)
	}
}

func TestRemoveClaudePingTranscript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	id := newClaudeSessionID()
	project := filepath.Join(dir, "projects", "-Users-me-work")
	keep := filepath.Join(project, "other.jsonl")
	for _, p := range []string{filepath.Join(project, id+".jsonl"), filepath.Join(project, id, "tool-results", "x.txt"), keep} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removeClaudePingTranscript(id)
	if _, err := os.Stat(filepath.Join(project, id+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("transcript still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, id)); !os.IsNotExist(err) {
		t.Fatalf("session dir still there: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("an unrelated transcript was removed: %v", err)
	}
}

func TestClaudeTriggerDryRunUsesInteractiveCommand(t *testing.T) {
	fakeClaudeHelp(t, "")
	c := NewClaude(config.ProviderConfig{
		Prompt: ".",
		Model:  "haiku",
		ExtraArgs: []string{
			"--max-turns", "1",
			"--output-format", "json",
		},
	})

	res, err := c.Trigger(context.Background(), true)
	if err != nil {
		t.Fatalf("dry-run trigger: %v", err)
	}
	if res.Command != "claude --model haiku -- ." {
		t.Fatalf("command = %q, want %q", res.Command, "claude --model haiku -- .")
	}
	if strings.Contains(res.Command, " -p") || strings.Contains(res.Command, "--print") {
		t.Fatalf("command still uses headless mode: %q", res.Command)
	}
}

func TestClaudeScopedLimitsKeepOnlyTheNarrowerRows(t *testing.T) {
	var r claudeUsageResp
	body := `{"limits":[
		{"kind":"session","percent":8,"resets_at":"2026-10-07T17:40:00+00:00"},
		{"kind":"weekly_all","percent":9,"resets_at":"2026-10-10T20:00:00+00:00"},
		{"kind":"weekly_scoped","percent":41,"resets_at":"2026-10-10T20:00:00+00:00","scope":{"model":{"display_name":"Opus"}}},
		{"kind":"weekly_scoped","percent":3,"resets_at":null,"scope":{"surface":{"display_name":"Cowork"}}},
		{"kind":"monthly_new_meter","percent":1,"resets_at":null,"scope":null}
	]}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	got := claudeScopedLimits(r.Limits)
	if len(got) != 3 {
		t.Fatalf("scoped = %+v, want the three non plan-wide rows", got)
	}
	if got[0].Label != "Opus" || got[0].Window.UsedPercent != 41 || got[0].Window.WindowSeconds != claudeWeeklySec || got[0].Window.ResetsAt.IsZero() {
		t.Fatalf("model row = %+v", got[0])
	}
	if got[1].Label != "Cowork" || got[2].Label != "monthly_new_meter" {
		t.Fatalf("labels = %q, %q: a row without a scope label falls back to its kind", got[1].Label, got[2].Label)
	}
}

func TestClaudeBucketsKeepOnlyReportedWindows(t *testing.T) {
	got := claudeBuckets([]byte(`{
		"five_hour": {"utilization": 8, "resets_at": "2026-10-07T17:40:00+00:00"},
		"seven_day": {"utilization": 9, "resets_at": "2026-10-10T20:00:00+00:00"},
		"seven_day_opus": {"utilization": 100, "resets_at": "2026-10-10T20:00:00+00:00"},
		"seven_day_sonnet": null,
		"seven_day_breakdown": {"as_of": "2026-10-07T14:17:28+00:00", "rows": []}
	}`))
	if len(got) != 1 {
		t.Fatalf("buckets = %+v, want only seven_day_opus", got)
	}
	opus := got["seven_day_opus"]
	if opus.UsedPercent != 100 || opus.WindowSeconds != claudeWeeklySec || opus.ResetsAt.IsZero() {
		t.Fatalf("seven_day_opus = %+v", opus)
	}
}

func TestNormalizedClaudeVersion(t *testing.T) {
	if got := normalizedClaudeVersion("2.1.168 (Claude Code)\n"); got != "2.1.168" {
		t.Fatalf("version = %q, want %q", got, "2.1.168")
	}
	if got := normalizedClaudeVersion(" \n\t"); got != "" {
		t.Fatalf("empty version = %q, want empty", got)
	}
}

func claudeDeniedResponse(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Body: io.NopCloser(strings.NewReader(`{
			"type":"error",
			"error":{
				"type":"permission_error",
				"message":"OAuth authentication is currently not allowed for this organization."
			}
		}`)),
		Request: req,
	}
}

func TestDiagnoseClaudeUsageErrorReportsSubscriptionAccess(t *testing.T) {
	requests := 0
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Method != http.MethodPost || req.URL.String() != claudeCountTokensURL {
			t.Fatalf("probe request = %s %s", req.Method, req.URL)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := req.Header.Get("anthropic-version"); got != claudeAPIVersion {
			t.Fatalf("anthropic-version = %q", got)
		}
		if got := req.Header.Get("anthropic-beta"); got != claudeOAuthBeta {
			t.Fatalf("anthropic-beta = %q", got)
		}
		if got := req.Header.Get("User-Agent"); !strings.HasPrefix(got, "claude-code/") {
			t.Fatalf("User-Agent = %q", got)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != claudeAccessProbeBody {
			t.Fatalf("probe body = %s", body)
		}
		return claudeDeniedResponse(req), nil
	})

	original := &UsageHTTPError{
		StatusCode: http.StatusTooManyRequests,
		Body:       `{"error":{"type":"rate_limit_error"}}`,
		RetryAfter: time.Now().Add(time.Hour),
	}
	got := diagnoseClaudeUsageError(context.Background(), staticTokenSource{token: "oauth-token"}, original)

	var accessErr *ClaudeSubscriptionAccessError
	if !errors.As(got, &accessErr) {
		t.Fatalf("error = %T %v, want ClaudeSubscriptionAccessError", got, got)
	}
	// The 429 must stay reachable: the scheduler pauses reads on its
	// Retry-After instead of falling into generic backoff.
	var httpErr *UsageHTTPError
	if !errors.As(got, &httpErr) || httpErr != original {
		t.Fatalf("subscription error does not unwrap to the original 429: %v", got)
	}
	if requests != 1 {
		t.Fatalf("probe requests = %d, want 1", requests)
	}
}

func TestDiagnoseClaudeUsageErrorPreservesRealRateLimit(t *testing.T) {
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"input_tokens":1}`)),
			Request:    req,
		}, nil
	})

	original := &UsageHTTPError{
		StatusCode: http.StatusTooManyRequests,
		Body:       `{"error":"slow down"}`,
		RetryAfter: time.Now().Add(time.Hour),
	}
	got := diagnoseClaudeUsageError(context.Background(), staticTokenSource{token: "oauth-token"}, original)
	if got != original {
		t.Fatalf("error = %T %v, want original 429", got, got)
	}
}

func TestDiagnoseClaudeUsageErrorPreserves429WhenProbeIsInconclusive(t *testing.T) {
	cases := []struct {
		name      string
		response  *http.Response
		transport error
	}{
		{
			name: "unrelated 403",
			response: &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"permission_error","message":"model is restricted"}}`)),
			},
		},
		{
			name: "stale token",
			response: &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"authentication_error","message":"token expired"}}`)),
			},
		},
		{
			name: "probe rate limited",
			response: &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"rate_limit_error"}}`)),
			},
		},
		{
			name:      "network failure",
			transport: errors.New("connection reset"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTransport(t, func(req *http.Request) (*http.Response, error) {
				if tc.transport != nil {
					return nil, tc.transport
				}
				tc.response.Request = req
				return tc.response, nil
			})
			original := &UsageHTTPError{StatusCode: http.StatusTooManyRequests, Body: "original"}
			got := diagnoseClaudeUsageError(context.Background(), staticTokenSource{token: "oauth-token"}, original)
			if got != original {
				t.Fatalf("error = %T %v, want original 429", got, got)
			}
		})
	}
}

func TestDiagnoseClaudeUsageErrorDoesNotProbeOtherFailures(t *testing.T) {
	requests := 0
	useTransport(t, func(req *http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("must not be called")
	})
	original := &UsageHTTPError{StatusCode: http.StatusServiceUnavailable, Body: "maintenance"}
	if got := diagnoseClaudeUsageError(context.Background(), staticTokenSource{token: "oauth-token"}, original); got != original {
		t.Fatalf("error = %v, want original", got)
	}
	if requests != 0 {
		t.Fatalf("probe requests = %d, want 0", requests)
	}
}

func TestClaudeSubscriptionDeniedResponse(t *testing.T) {
	canonical := `{"error":{"type":"permission_error","message":"OAuth authentication is currently not allowed for this organization."}}`
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{name: "oauth denied 403", status: 403, body: canonical, want: true},
		{name: "oauth denied 401", status: 401, body: canonical, want: true},
		{name: "error code", status: 403, body: `{"error":{"code":"oauth_org_not_allowed"}}`, want: true},
		{name: "subscription disabled", status: 403,
			body: `{"error":{"message":"` + claudeDisabledText + `"}}`, want: true},
		{name: "unrelated denial", status: 403, body: `{"error":{"type":"permission_error","message":"model denied"}}`},
		{name: "wrong status", status: 429, body: canonical},
		{name: "empty body", status: 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeSubscriptionDeniedResponse(tc.status, []byte(tc.body)); got != tc.want {
				t.Fatalf("denied = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestClaudeInteractiveErrRecognizesSubscriptionDenial(t *testing.T) {
	// Claude Code renders this error inside a bordered box, so the sentence
	// reaches us coloured and word-wrapped.
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "single line",
			output: "\x1b[31mYour organization has disabled Claude subscription access for Claude Code\x1b[0m",
			want:   true,
		},
		{
			name: "wrapped in a TUI box",
			output: "╭────────────────────────────────────╮\r\n" +
				"│ \x1b[31mYour organization has disabled Claude\x1b[0m │\r\n" +
				"│ \x1b[31msubscription access for Claude Code\x1b[0m   │\r\n" +
				"╰────────────────────────────────────╯\r\n",
			want: true,
		},
		{name: "ordinary output", output: "Rate limited. Please try again later."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := &limitedBuffer{limit: 4096}
			_, _ = output.Write([]byte(tc.output))
			err := claudeInteractiveErr(nil, output)
			var accessErr *ClaudeSubscriptionAccessError
			if errors.As(err, &accessErr) != tc.want {
				t.Fatalf("error = %T %v, want denial = %t", err, err, tc.want)
			}
		})
	}
}
