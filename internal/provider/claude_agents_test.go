package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/wavever/CCLimitPing/internal/activity"
	"github.com/wavever/CCLimitPing/internal/config"
)

func fakeClaudeAgents(t *testing.T, out string, err error) {
	t.Helper()
	old := claudeAgentsOutput
	claudeAgentsOutput = func(context.Context) ([]byte, error) { return []byte(out), err }
	t.Cleanup(func() { claudeAgentsOutput = old })
}

const claudeAgentsSample = `[
  {"pid": 11, "cwd": "/a", "kind": "interactive", "sessionId": "d1fd8dec-48d7", "name": "clipboard", "status": "idle"},
  {"pid": 12, "id": "b6344a1d", "cwd": "/b", "kind": "background", "sessionId": "b6344a1d-9e83", "name": "", "status": "busy", "state": "working"}
]`

func TestClaudeActiveTaskFallsBackToSessionList(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no hooks installed
	fakeClaudeAgents(t, claudeAgentsSample, nil)

	desc, active, err := NewClaude(config.ProviderConfig{}).ActiveTask(context.Background())
	if err != nil || !active || desc != "session b6344a1d" {
		t.Fatalf("ActiveTask = %q, %t, %v; want the busy background session", desc, active, err)
	}
}

func TestClaudeActiveTaskWithoutSessionListJustPings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fakeClaudeAgents(t, "", errors.New("unknown command 'agents'"))

	if _, active, err := NewClaude(config.ProviderConfig{}).ActiveTask(context.Background()); active || err != nil {
		t.Fatalf("active=%t err=%v: an old Claude Code must not block or fail the ping", active, err)
	}
}

func TestClaudeActiveTaskPrefersHooks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := activity.SetEnabled("claude", true); err != nil {
		t.Fatal(err)
	}
	fakeClaudeAgents(t, claudeAgentsSample, nil)

	if _, active, _ := NewClaude(config.ProviderConfig{}).ActiveTask(context.Background()); active {
		t.Fatal("with hooks installed their signal decides, and no hook marked a session active")
	}
}
