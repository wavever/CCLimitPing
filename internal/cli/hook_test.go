package cli

import (
	"strings"
	"testing"

	"github.com/wavever/CCLimitPing/internal/activity"
)

func TestRecordHookEventRunningThenStop(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	recordHookEvent(strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit"}`), "claude")
	if _, active, _ := activity.Active("claude"); !active {
		t.Fatal("UserPromptSubmit should mark the session active")
	}

	recordHookEvent(strings.NewReader(`{"session_id":"s1","hook_event_name":"Stop"}`), "claude")
	if _, active, _ := activity.Active("claude"); active {
		t.Fatal("Stop should clear the session")
	}
}

// A turn can end without Stop: Claude Code fires StopFailure when an API error
// (a usage limit, typically) ends it, and Codex fires Interrupt when the user
// cuts it short. Either must clear the session, not leave it "active" until the
// TTL runs out.
func TestRecordHookEventTurnEndsWithoutStop(t *testing.T) {
	for _, tc := range []struct{ provider, event string }{
		{"claude", "StopFailure"},
		{"codex", "Interrupt"},
		{"codex", "SessionEnd"},
	} {
		t.Run(tc.provider+"/"+tc.event, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			recordHookEvent(strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit"}`), tc.provider)
			recordHookEvent(strings.NewReader(`{"session_id":"s1","hook_event_name":"`+tc.event+`"}`), tc.provider)
			if _, active, _ := activity.Active(tc.provider); active {
				t.Fatalf("%s should clear the session", tc.event)
			}
		})
	}
}

func TestRecordHookEventToleratesBadInput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// None of these should panic or mark anything active.
	recordHookEvent(strings.NewReader(""), "claude")
	recordHookEvent(strings.NewReader("not json"), "claude")
	recordHookEvent(strings.NewReader(`{"hook_event_name":"Notification"}`), "claude")
	if _, active, _ := activity.Active("claude"); active {
		t.Fatal("bad/ignored input must not mark active")
	}
}
