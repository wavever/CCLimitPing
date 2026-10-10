package spend

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// claudeLine renders one assistant transcript entry the way Claude Code writes
// them, so the fixtures read like the files this parses.
func claudeLine(stamp time.Time, msgID, requestID, model string, in, cacheWrite, cacheRead, out int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":%q,"sessionId":"s1","message":`+
		`{"id":%q,"model":%q,"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,`+
		`"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		stamp.UTC().Format(time.RFC3339Nano), requestID, msgID, model, in, cacheWrite, cacheRead, out)
}

func TestReadClaudeCountsOneEntryPerRequest(t *testing.T) {
	now := time.Now()
	dir := writeClaudeTranscript(t, "project-a", "session.jsonl", []string{
		// Streaming writes the same assistant message repeatedly; only one of
		// these was a billed request.
		claudeLine(now, "msg_1", "req_1", "claude-opus-5", 10, 100, 1000, 50),
		claudeLine(now, "msg_1", "req_1", "claude-opus-5", 10, 100, 1000, 50),
		claudeLine(now, "msg_2", "req_2", "claude-opus-5", 5, 0, 2000, 25),
		claudeLine(now, "msg_3", "req_3", "claude-haiku-4-5", 1, 0, 3, 7),
		// Yesterday's turn, still in a transcript touched today.
		claudeLine(now.AddDate(0, 0, -1), "msg_0", "req_0", "claude-opus-5", 999, 999, 999, 999),
		// A local error rendered as an assistant turn never reached the API.
		claudeLine(now, "msg_4", "req_4", "<synthetic>", 1, 1, 1, 1),
	})
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	start := startOfDay(now)
	byModel, available, err := readDay(readClaude, start)
	if err != nil {
		t.Fatalf("readClaude() error = %v", err)
	}
	if !available {
		t.Fatal("readClaude() available = false, want true for an existing projects dir")
	}
	opus := byModel["claude-opus-5"]
	if opus.Input != 15 || opus.CacheWrite != 100 || opus.CacheRead != 3000 || opus.Output != 75 {
		t.Fatalf("claude-opus-5 tokens = %+v, want the two of today's requests counted once each", opus)
	}
	if got := byModel["claude-haiku-4-5"].Total(); got != 11 {
		t.Fatalf("claude-haiku-4-5 total = %d, want 11", got)
	}
	if _, ok := byModel["<synthetic>"]; ok {
		t.Fatal("synthetic entries were counted, want them skipped")
	}
}

func TestReadClaudeKeepsTheFinalLineOfAStreamedReply(t *testing.T) {
	now := time.Now()
	dir := writeClaudeTranscript(t, "project-a", "session.jsonl", []string{
		// The first line of a streamed reply is written before the reply is
		// done, so its output count is a fraction of the final one.
		claudeLine(now, "msg_1", "req_1", "claude-opus-5", 10, 100, 1000, 6),
		claudeLine(now, "msg_1", "req_1", "claude-opus-5", 10, 100, 1000, 275),
	})
	// A subagent's sidechain can repeat the request with a stale count.
	writeClaudeTranscriptIn(t, dir, "project-a", "agent-a1.jsonl", []string{
		claudeLine(now, "msg_1", "req_1", "claude-opus-5", 10, 100, 1000, 6),
	})
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	byModel, _, err := readDay(readClaude, startOfDay(now))
	if err != nil {
		t.Fatalf("readClaude() error = %v", err)
	}
	if got := byModel["claude-opus-5"]; got.Output != 275 || got.Input != 10 || got.CacheRead != 1000 {
		t.Fatalf("tokens = %+v, want the request once, with its final output", got)
	}
}

func TestReadClaudeSeparatesOneHourCacheWrites(t *testing.T) {
	now := time.Now()
	line := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":"req_1","message":{"id":"msg_1",`+
		`"model":"claude-opus-5","usage":{"input_tokens":1,"cache_creation_input_tokens":300,`+
		`"cache_read_input_tokens":0,"output_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":100,`+
		`"ephemeral_1h_input_tokens":200}}}}`, now.UTC().Format(time.RFC3339Nano))
	t.Setenv("CLAUDE_CONFIG_DIR", writeClaudeTranscript(t, "project-a", "session.jsonl", []string{line}))

	byModel, _, err := readDay(readClaude, startOfDay(now))
	if err != nil {
		t.Fatalf("readClaude() error = %v", err)
	}
	if got := byModel["claude-opus-5"]; got.CacheWrite != 300 || got.CacheWrite1h != 200 {
		t.Fatalf("tokens = %+v, want 300 cache writes, 200 of them for an hour", got)
	}
}

func TestReadClaudeSkipsTranscriptsUntouchedToday(t *testing.T) {
	now := time.Now()
	dir := writeClaudeTranscript(t, "project-a", "old.jsonl", []string{
		claudeLine(now, "msg_1", "req_1", "claude-opus-5", 10, 0, 0, 10),
	})
	// A transcript is append-only, so one last written before today cannot hold
	// today's turns — whatever its contents claim.
	path := filepath.Join(dir, "projects", "project-a", "old.jsonl")
	stale := now.AddDate(0, 0, -2)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	start := startOfDay(now)
	byModel, _, err := readDay(readClaude, start)
	if err != nil {
		t.Fatalf("readClaude() error = %v", err)
	}
	if len(byModel) != 0 {
		t.Fatalf("byModel = %v, want nothing from a transcript untouched today", byModel)
	}
}

func TestReadClaudeReportsUnavailableWithoutAProjectsDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	start := startOfDay(time.Now())
	_, available, err := readDay(readClaude, start)
	if err != nil {
		t.Fatalf("readClaude() error = %v", err)
	}
	if available {
		t.Fatal("readClaude() available = true, want false when Claude Code has never run here")
	}
}

func TestClaudeRootsSplitsTheConfigDirList(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/one, /two ,")
	if got := claudeRoots(); len(got) != 2 || got[0] != "/one" || got[1] != "/two" {
		t.Fatalf("claudeRoots() = %v, want both entries trimmed", got)
	}
}

func writeClaudeTranscript(t *testing.T, project, name string, lines []string) string {
	t.Helper()
	root := t.TempDir()
	writeClaudeTranscriptIn(t, root, project, name, lines)
	return root
}

func writeClaudeTranscriptIn(t *testing.T, root, project, name string, lines []string) {
	t.Helper()
	dir := filepath.Join(root, "projects", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := ""
	for _, line := range lines {
		body += line + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
