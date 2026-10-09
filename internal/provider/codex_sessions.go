package provider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wavever/CCLimitPing/internal/auth"
)

// codexRolloutTTL bounds how stale a rollout may be and still count as a live
// turn. It matches the hook markers' TTL: a turn writes to its rollout as items
// complete, so one left mid-turn and untouched this long belongs to a Codex
// that exited without ending it (killed, crashed) rather than one still working.
const codexRolloutTTL = 10 * time.Minute

// codexBusySession is the hook-less answer to "is a Codex turn running": Codex's
// own session log. Every session — TUI, `codex exec`, the desktop app — appends
// to a rollout under <CODEX_HOME>/sessions, opening each turn with a
// task_started event and closing it with task_complete or turn_aborted (checked
// across 1,600 turns on 2026-10-09: nothing else ends one). A recently written
// rollout whose last turn is still open is a turn in progress. The ping itself
// runs `codex exec --ephemeral`, which writes no rollout, so it can never count
// as one. Any problem reading the logs reads as "not busy": the scheduler then
// pings, which is what it did before this check existed.
func codexBusySession(now time.Time) (string, bool) {
	home, err := auth.CodexHome()
	if err != nil {
		return "", false
	}
	return codexBusyRollout(filepath.Join(home, "sessions"), now)
}

func codexBusyRollout(dir string, now time.Time) (string, bool) {
	cutoff := now.Add(-codexRolloutTTL)
	var busy string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		if codexRolloutTurnOpen(path) {
			busy = "session " + codexRolloutLabel(name)
			return fs.SkipAll
		}
		return nil
	})
	return busy, busy != ""
}

// codexRolloutTurnOpen reports whether the rollout's last turn has started and
// not ended. Lines are decoded rather than substring-matched, since a turn's
// own content can quote these event names.
func codexRolloutTurnOpen(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	open := false
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, []byte(`"event_msg"`)) {
			var rec struct {
				Type    string `json:"type"`
				Payload struct {
					Type string `json:"type"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &rec) == nil && rec.Type == "event_msg" {
				switch rec.Payload.Type {
				case "task_started":
					open = true
				case "task_complete", "turn_aborted":
					open = false
				}
			}
		}
		if err == io.EOF {
			return open
		}
		if err != nil {
			return false
		}
	}
}

// codexRolloutLabel shortens rollout-<timestamp>-<uuid>.jsonl to the start of
// the session id, the way the hook markers are labelled.
func codexRolloutLabel(name string) string {
	id := strings.TrimSuffix(name, ".jsonl")
	if len(id) >= 36 {
		id = id[len(id)-36:]
	}
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}
