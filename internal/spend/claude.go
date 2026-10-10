package spend

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wavever/CCLimitPing/internal/pricing"
)

// syntheticModel marks a Claude Code transcript entry that never reached the
// API (a local error message rendered as an assistant turn). It has no cost and
// no tokens to count.
const syntheticModel = "<synthetic>"

// claudeEntry is the part of a Claude Code transcript line that carries usage.
type claudeEntry struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	RequestID string `json:"requestId"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			// CacheCreation splits the cache writes by TTL; Claude Code asks
			// for the one-hour cache, which bills above the five-minute one.
			CacheCreation struct {
				OneHour int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// claudeRequest is one API request's usage, as its fullest transcript line
// reports it.
type claudeRequest struct {
	at     time.Time
	model  string
	tokens pricing.Tokens
}

// readClaude feeds add every Claude Code request made at or after since,
// reporting whether Claude Code has a transcript directory here at all.
func readClaude(since time.Time, add sink) (bool, error) {
	// Claude Code writes the same assistant message once per stream update, so
	// roughly half the usage lines in a transcript are repeats of a request.
	// Only the last carries the final output count — an early one can report a
	// handful of tokens for a reply that ran to hundreds — so each request is
	// held until every file is read and the line with the most output wins.
	// Keyed on the API's own ids, not on the file position.
	requests := map[string]*claudeRequest{}
	available := false
	var firstErr error

	for _, root := range claudeRoots() {
		projects := filepath.Join(root, "projects")
		if !dirExists(projects) {
			continue
		}
		available = true
		files, err := transcripts(projects, since)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, path := range files {
			if err := readClaudeFile(path, since, requests, add); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for _, r := range requests {
		add(r.at, r.model, r.tokens)
	}
	return available, firstErr
}

// readClaudeFile collects path's requests into requests, keeping the fullest
// line of each, and feeds the few lines with no ids to add straight away.
func readClaudeFile(path string, since time.Time, requests map[string]*claudeRequest, add sink) error {
	marker := []byte(`"usage"`)
	return forEachLine(path, func(line []byte) {
		if !bytes.Contains(line, marker) {
			return
		}
		var e claudeEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return
		}
		if e.Type != "assistant" || e.Message.Model == "" || e.Message.Model == syntheticModel {
			return
		}
		at, ok := stampSince(e.Timestamp, since)
		if !ok {
			return
		}
		u := e.Message.Usage
		tokens := pricing.Tokens{
			Input:        u.InputTokens,
			CacheRead:    u.CacheReadInputTokens,
			CacheWrite:   u.CacheCreationInputTokens,
			CacheWrite1h: u.CacheCreation.OneHour,
			Output:       u.OutputTokens,
		}
		key := e.Message.ID + "|" + e.RequestID
		if key == "|" {
			add(at, e.Message.Model, tokens)
			return
		}
		if r := requests[key]; r != nil {
			if tokens.Output > r.tokens.Output {
				r.tokens = tokens
			}
			return
		}
		requests[key] = &claudeRequest{at: at, model: e.Message.Model, tokens: tokens}
	})
}

// claudeRoots returns the directories that hold Claude Code's transcripts:
// $CLAUDE_CONFIG_DIR when set (the CLI accepts a comma-separated list), else
// both well-known locations.
func claudeRoots() []string {
	if v := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); v != "" {
		var out []string
		for _, dir := range strings.Split(v, ",") {
			if dir = strings.TrimSpace(dir); dir != "" {
				out = append(out, dir)
			}
		}
		return out
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".config", "claude"),
	}
}
