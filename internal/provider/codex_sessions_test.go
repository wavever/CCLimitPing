package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testRolloutName = "rollout-2026-10-09T16-05-17-01a11fb1-f08b-70b1-aa83-d606008685e3.jsonl"

func writeRollout(t *testing.T, dir, name string, events []string, mtime time.Time) {
	t.Helper()
	day := filepath.Join(dir, "2026", "10", "09")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"type":"session_meta","payload":{"id":"x"}}` + "\n")
	for _, ev := range events {
		b.WriteString(`{"type":"event_msg","payload":{"type":"` + ev + `"}}` + "\n")
	}
	path := filepath.Join(day, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestCodexBusyRolloutFindsAnOpenTurn(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		events []string
		mtime  time.Time
		want   bool
	}{
		{"turn running", []string{"task_started", "task_complete", "task_started"}, now, true},
		{"turn completed", []string{"task_started", "task_complete"}, now, false},
		{"turn interrupted", []string{"task_started", "turn_aborted"}, now, false},
		{"session opened, no turn yet", nil, now, false},
		// A Codex killed mid-turn never closes it; the age is what retires it.
		{"abandoned mid-turn", []string{"task_started"}, now.Add(-codexRolloutTTL - time.Minute), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeRollout(t, dir, testRolloutName, c.events, c.mtime)
			desc, busy := codexBusyRollout(dir, now)
			if busy != c.want {
				t.Fatalf("busy = %v, want %v", busy, c.want)
			}
			if busy && desc != "session 01a11fb1" {
				t.Fatalf("desc = %q, want the session id's prefix", desc)
			}
		})
	}
}

// A turn's own content can name these events — a conversation about this very
// code, say. Only real event records may move the turn state.
func TestCodexBusyRolloutIgnoresEventNamesInContent(t *testing.T) {
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "10", "09")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"event_msg","payload":{"type":"task_started"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"task_complete"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","text":"{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\"}}"}}` + "\n"
	if err := os.WriteFile(filepath.Join(day, testRolloutName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, busy := codexBusyRollout(dir, time.Now()); busy {
		t.Fatal("a quoted event name was read as a turn starting")
	}
}

func TestCodexBusyRolloutWithoutSessions(t *testing.T) {
	if _, busy := codexBusyRollout(filepath.Join(t.TempDir(), "missing"), time.Now()); busy {
		t.Fatal("a missing sessions directory read as busy")
	}
}
