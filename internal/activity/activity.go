// Package activity tracks whether a provider's CLI session is mid-turn, using
// signals written by limitping's hook command (see `limitping hook`). It is the
// hook-based replacement for process scanning: a session counts as active only
// between a prompt submission and the turn's Stop, not merely because the CLI
// process exists.
//
// State lives under config.Dir()/activity:
//
//	activity/<provider>/<session>.json  per-session marker {event, updated_at}
//	activity/<provider>.enabled         sentinel: trust the hook signal
package activity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
)

// activityTTL bounds how long a session marker is trusted without a refresh.
// Running events refresh it throughout a turn; the TTL self-heals sessions
// killed mid-turn before Stop could fire.
const activityTTL = 10 * time.Minute

// runningEvents refresh a session marker; stopEvents remove it. Both Claude Code
// and Codex emit these names. A turn does not always end in Stop: Claude Code
// fires StopFailure instead when an API error (a usage limit, say) ends it, and
// Codex fires Interrupt when the user cuts it short. The TTL still covers a
// session killed before any of them could fire.
var runningEvents = map[string]bool{
	"UserPromptSubmit": true,
	"PreToolUse":       true,
	"PostToolUse":      true,
}

var stopEvents = map[string]bool{
	"Stop":        true,
	"StopFailure": true,
	"Interrupt":   true,
	"SessionEnd":  true,
}

// IsRunningEvent reports whether a hook event means the session is mid-turn.
func IsRunningEvent(event string) bool { return runningEvents[event] }

// IsStopEvent reports whether a hook event means the session's turn ended.
func IsStopEvent(event string) bool { return stopEvents[event] }

type marker struct {
	Event     string    `json:"event"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Mark records that sessionID for provider is mid-turn. Called on running events.
func Mark(provider, sessionID, event string) error {
	dir, err := providerDir(provider)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(marker{Event: event, UpdatedAt: time.Now()})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, sessionFile(sessionID)), data, 0o644)
}

// Clear removes the marker for sessionID. Called on stop events. A missing
// marker is not an error.
func Clear(provider, sessionID string) error {
	dir, err := providerDir(provider)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, sessionFile(sessionID))); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Active reports whether any session of provider is mid-turn. A marker counts as
// live when its recorded time is within activityTTL and its event is a running
// event; stale markers are pruned opportunistically.
func Active(provider string) (string, bool, error) {
	dir, err := providerDir(provider)
	if err != nil {
		return "", false, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}

	cutoff := time.Now().Add(-activityTTL)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		m, err := readMarker(path)
		if err != nil {
			continue
		}
		if m.UpdatedAt.Before(cutoff) {
			_ = os.Remove(path) // prune: session likely died before Stop fired
			continue
		}
		if !runningEvents[m.Event] {
			continue
		}
		session := strings.TrimSuffix(e.Name(), ".json")
		if len(session) > 8 {
			session = session[:8]
		}
		return fmt.Sprintf("session %s", session), true, nil
	}
	return "", false, nil
}

// Enabled reports whether hook-based detection has been installed for provider.
func Enabled(provider string) bool {
	path, err := sentinelPath(provider)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// SetEnabled creates or removes the provider's sentinel, marking whether the
// hook signal should be trusted over the process-scan fallback.
func SetEnabled(provider string, enabled bool) error {
	path, err := sentinelPath(provider)
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644)
}

func readMarker(path string) (marker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return marker{}, err
	}
	var m marker
	if err := json.Unmarshal(data, &m); err != nil {
		return marker{}, err
	}
	return m, nil
}

func root() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "activity"), nil
}

func providerDir(provider string) (string, error) {
	r, err := root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, sanitize(provider)), nil
}

func sentinelPath(provider string) (string, error) {
	r, err := root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, sanitize(provider)+".enabled"), nil
}

func sessionFile(sessionID string) string {
	return sanitize(sessionID) + ".json"
}

// sanitize reduces an untrusted id to a safe single path component.
func sanitize(s string) string {
	s = filepath.Base(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}
