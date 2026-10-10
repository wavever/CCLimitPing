package spend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/wavever/CCLimitPing/internal/auth"
	"github.com/wavever/CCLimitPing/internal/pricing"
)

// codexLine is the envelope every rollout record shares.
type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// codexUsage is one request's token counts as Codex records them. InputTokens
// is the whole prompt; CachedInputTokens is the part of it served from cache
// and CacheWriteInputTokens the part written to it.
type codexUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	CacheWriteInputTokens int `json:"cache_write_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
}

// tokens splits u into billing buckets, clamped so a malformed record can
// never invent tokens the prompt did not have.
func (u codexUsage) tokens() pricing.Tokens {
	total := max(u.InputTokens, 0)
	cached := min(max(u.CachedInputTokens, 0), total)
	written := min(max(u.CacheWriteInputTokens, 0), total-cached)
	return pricing.Tokens{
		Input:      total - cached - written,
		CacheRead:  cached,
		CacheWrite: written,
		Output:     u.OutputTokens,
	}
}

// codexRecord is one priced request pulled out of a rollout, tagged with the
// model in effect when it was made.
type codexRecord struct {
	key    string
	at     time.Time
	model  string
	tokens pricing.Tokens
}

// readCodex feeds add every Codex request made at or after since, reporting
// whether Codex has a sessions directory here at all.
func readCodex(since time.Time, add sink) (bool, error) {
	root := codexSessionsDir()
	if root == "" || !dirExists(root) {
		return false, nil
	}
	files, firstErr := transcripts(root, since)
	// Resuming a thread forks a new rollout that replays the records already
	// written to the old one, so requests are deduplicated across files.
	seen := map[string]bool{}
	for _, path := range files {
		records, err := readCodexFile(path, since)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, r := range records {
			if seen[r.key] {
				continue
			}
			seen[r.key] = true
			add(r.at, r.model, r.tokens)
		}
	}
	return true, firstErr
}

// readCodexFile extracts one rollout's requests. Codex 0.15x writes an explicit
// token_usage_record per response; older versions only emit token_count events,
// whose last_token_usage is the delta since the previous one. Both appear in a
// single file only while a version straddles them, so the explicit records win
// when present rather than being added to the deltas.
func readCodexFile(path string, since time.Time) ([]codexRecord, error) {
	var records, deltas []codexRecord
	model := ""

	err := forEachLine(path, func(line []byte) {
		if !bytes.Contains(line, []byte("token_")) &&
			!bytes.Contains(line, []byte("turn_context")) &&
			!bytes.Contains(line, []byte("session_meta")) {
			return
		}
		var l codexLine
		if err := json.Unmarshal(line, &l); err != nil || len(l.Payload) == 0 {
			return
		}
		switch l.Type {
		case "session_meta", "turn_context":
			// The model can be switched mid-session, so every turn restates it;
			// session_meta covers the older rollouts that had no turn_context.
			var p struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(l.Payload, &p) == nil && p.Model != "" {
				model = p.Model
			}
		case "token_usage_record":
			var p struct {
				ResponseID string     `json:"response_id"`
				Usage      codexUsage `json:"usage"`
			}
			if json.Unmarshal(l.Payload, &p) != nil {
				return
			}
			at, ok := stampSince(l.Timestamp, since)
			if !ok {
				return
			}
			key := p.ResponseID
			if key == "" {
				key = fmt.Sprintf("%s|%d", l.Timestamp, len(records))
			}
			records = append(records, codexRecord{key: key, at: at, model: model, tokens: p.Usage.tokens()})
		case "event_msg":
			var p struct {
				Type string `json:"type"`
				Info *struct {
					LastTokenUsage codexUsage `json:"last_token_usage"`
				} `json:"info"`
			}
			if json.Unmarshal(l.Payload, &p) != nil || p.Type != "token_count" || p.Info == nil {
				return
			}
			at, ok := stampSince(l.Timestamp, since)
			if !ok {
				return
			}
			u := p.Info.LastTokenUsage
			// One key per event: two turns cannot share a millisecond stamp and
			// an identical token split, so a replayed record collapses onto the
			// original instead of being counted twice.
			key := fmt.Sprintf("%s|%d|%d|%d", l.Timestamp, u.InputTokens, u.CachedInputTokens, u.OutputTokens)
			deltas = append(deltas, codexRecord{key: key, at: at, model: model, tokens: u.tokens()})
		}
	})
	if len(records) > 0 {
		return records, err
	}
	return deltas, err
}

// codexSessionsDir returns the rollout directory: $CODEX_HOME/sessions, else
// ~/.codex/sessions.
func codexSessionsDir() string {
	dir, err := auth.CodexHome()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "sessions")
}
