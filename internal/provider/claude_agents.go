package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// ClaudeSession is one live Claude Code session as `claude agents --json`
// lists it: interactive ones and background (--bg) ones alike.
type ClaudeSession struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	// Status is "busy" while a turn is running and "idle" otherwise.
	Status string `json:"status"`
}

// Busy reports whether the session is mid-turn.
func (s ClaudeSession) Busy() bool { return s.Status == "busy" }

const claudeAgentsTimeout = 5 * time.Second

// claudeAgentsOutput runs `claude agents --json`; tests swap it for a fake.
var claudeAgentsOutput = func(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeAgentsTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "claude", "agents", "--json").Output()
}

// ClaudeSessions lists the live Claude Code sessions. It is Claude Code's own
// account of which sessions are running a turn, so unlike a process scan it
// neither mistakes an idle REPL for a busy one nor counts unrelated programs
// built on the agent SDK. Claude Code releases without the command fail here.
func ClaudeSessions(ctx context.Context) ([]ClaudeSession, error) {
	out, err := claudeAgentsOutput(ctx)
	if err != nil {
		return nil, fmt.Errorf("claude agents --json: %w", err)
	}
	var sessions []ClaudeSession
	if err := json.Unmarshal(out, &sessions); err != nil {
		return nil, fmt.Errorf("claude agents --json: parsing output: %w", err)
	}
	return sessions, nil
}

// claudeBusySession describes the first session running a turn, if any.
func claudeBusySession(sessions []ClaudeSession) (string, bool) {
	for _, s := range sessions {
		if !s.Busy() {
			continue
		}
		label := s.Name
		if label == "" {
			label = s.SessionID
			if len(label) > 8 {
				label = label[:8]
			}
		}
		return "session " + label, true
	}
	return "", false
}
