package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/codexstate"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

func TestBusyPingOutputSuggestsRetry(t *testing.T) {
	var out bytes.Buffer
	report(&out, enText, "codex", time.Now(), nil, fmt.Errorf("ping not sent: %w", codexstate.ErrBusy))
	want := "ping not sent: another ping or quota-state update is in progress.\nPlease try again in about a minute."
	if got := out.String(); !strings.Contains(got, want) || strings.Contains(got, "✓") {
		t.Fatal(got)
	}
}

func TestPingStartupHint(t *testing.T) {
	for _, text := range []cliText{enText, zhText} {
		for _, name := range []string{"codex", "spark"} {
			for _, tc := range []struct {
				err  error
				hint bool
			}{
				{fmt.Errorf("wrapped: %w", &provider.CodexCompletionError{Reason: "completion unconfirmed"}), true},
				{&provider.UsageHTTPError{StatusCode: 401}, false},
				{fmt.Errorf("process failed"), false},
				{codexstate.ErrBusy, false},
			} {
				var out bytes.Buffer
				res := &provider.TriggerResult{Verification: &usage.Verification{
					Target: "weekly", Weekly: usage.StartStatus{State: "started"},
				}}
				report(&out, text, name, time.Now(), res, tc.err)
				got := out.String()
				if strings.Contains(got, text.pingStartupHint) != tc.hint {
					t.Fatal(got)
				}
				if tc.hint && strings.Index(got, text.pingStartupHint) > strings.Index(got, fmt.Sprintf(text.verifyQuotaStateFmt, "weekly", text.verifyStarted)) {
					t.Fatal(got)
				}
			}
		}
	}
}

func TestPrecheckFailureOutputSaysNotSent(t *testing.T) {
	var out bytes.Buffer
	err := fmt.Errorf("ping not sent: quota precheck failed: %w", &provider.UsageHTTPError{StatusCode: 403})
	report(&out, enText, "codex", time.Now(), nil, err)
	if got := out.String(); !strings.Contains(got, "ping not sent: quota precheck failed") || !strings.Contains(got, "403") || strings.Contains(got, "✓") {
		t.Fatal(got)
	}
}

func TestVerificationGuidance(t *testing.T) {
	for _, tc := range []struct {
		name, state       string
		baseline, enabled bool
		want              string
	}{
		{"pending", "unknown", true, true, "after"},
		{"no-baseline", "unknown", false, true, "collect a baseline"},
		{"disabled", "unknown", true, false, "disabled"},
		{"confirmed", "started", false, true, "window started"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &usage.Verification{Target: "weekly", Weekly: usage.StartStatus{State: tc.state}}
			if tc.baseline {
				v.Weekly.DueAt = time.Now().Add(time.Minute)
			}
			var out bytes.Buffer
			reportVerification(&out, enText, &provider.TriggerResult{Verification: v, StatusEnabled: tc.enabled})
			if !strings.Contains(out.String(), tc.want) {
				t.Fatal(out.String())
			}
			if tc.state == "started" && strings.Contains(out.String(), "Run `") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestStartJSONDoesNotChangeLegacyActiveOrClaude(t *testing.T) {
	u := &usage.Usage{Provider: "codex", Weekly: usage.Window{ResetsAt: time.Now().Add(time.Hour), WindowSeconds: 604800},
		Verification: &usage.Verification{Weekly: usage.StartStatus{State: "started"}}}
	j := newStatusJSON(u, false)
	if j.Weekly.Active || j.Weekly.StartState != "started" {
		t.Fatal(j.Weekly)
	}
	u.Verification = nil
	u.Provider = "claude"
	j = newStatusJSON(u, false)
	if j.Weekly.StartState != "" {
		t.Fatal(j.Weekly)
	}
}

func TestBackgroundVerificationDoesNotCountAsPing(t *testing.T) {
	for _, tc := range []struct {
		msg   string
		count bool
	}{
		{"ping request completed; checking window", false},
		{"ping trigger returned; checking window", true},
		{"ping turn completed; checking window", true},
		{"window started after verification", false},
		{"quota read failed: timeout", false},
		{"ping failed: notification timeout; verifying quota before retry", true},
		{"ping sent, new window started", true},
	} {
		_, ok := parseBgPingAttempt("2026/09/01 12:00:00 [codex] " + tc.msg)
		if ok != tc.count {
			t.Fatalf("%s: %v", tc.msg, ok)
		}
	}
}

func TestReturnedTriggerDoesNotClaimTurnCompletion(t *testing.T) {
	var out bytes.Buffer
	report(&out, enText, "codex", time.Now(), &provider.TriggerResult{
		Verification: &usage.Verification{Target: "weekly", Weekly: usage.StartStatus{State: "unknown"}},
	}, nil)
	if !strings.Contains(out.String(), "CLI trigger returned without error") ||
		!strings.Contains(out.String(), "turn completion is not verified") {
		t.Fatal(out.String())
	}
}

func TestConfirmedTurnDoesNotClaimWindowStarted(t *testing.T) {
	var out bytes.Buffer
	report(&out, enText, "codex", time.Now(), &provider.TriggerResult{
		TurnCompleted: true,
		Verification:  &usage.Verification{Target: "weekly", Weekly: usage.StartStatus{State: "unknown"}},
	}, nil)
	if !strings.Contains(out.String(), "turn completed") || !strings.Contains(out.String(), "window start unconfirmed") {
		t.Fatal(out.String())
	}
	if strings.Contains(out.String(), "window started") {
		t.Fatal(out.String())
	}
}
