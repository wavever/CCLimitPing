package spend

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func codexTurnContext(stamp time.Time, model string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"model":%q,"cwd":"/tmp"}}`,
		stamp.UTC().Format(time.RFC3339Nano), model)
}

// codexRecordLine is the per-response record Codex 0.15x writes.
func codexRecordLine(stamp time.Time, responseID string, in, cached, out int) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"token_usage_record","payload":{"response_id":%q,`+
		`"usage":{"input_tokens":%d,"cached_input_tokens":%d,"cache_write_input_tokens":0,`+
		`"output_tokens":%d,"total_tokens":%d}}}`,
		stamp.UTC().Format(time.RFC3339Nano), responseID, in, cached, out, in+out)
}

// codexCountLine is the older token_count event, whose last_token_usage is the
// delta since the previous one.
func codexCountLine(stamp time.Time, in, cached, out int) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":`+
		`{"total_token_usage":{"input_tokens":0},"last_token_usage":{"input_tokens":%d,`+
		`"cached_input_tokens":%d,"cache_write_input_tokens":0,"output_tokens":%d}}}}`,
		stamp.UTC().Format(time.RFC3339Nano), in, cached, out)
}

func TestReadCodexUsesPerResponseRecords(t *testing.T) {
	now := time.Now()
	dir := writeCodexRollout(t, "rollout-a.jsonl", []string{
		codexTurnContext(now, "gpt-5.6-terra"),
		codexRecordLine(now, "resp_1", 20000, 5000, 200),
		codexRecordLine(now, "resp_2", 21000, 19000, 300),
		// A model switch mid-session splits the day between the two.
		codexTurnContext(now, "gpt-5.6-sol"),
		codexRecordLine(now, "resp_3", 1000, 0, 10),
		// Yesterday's turn, in a rollout still being appended to today.
		codexRecordLine(now.AddDate(0, 0, -1), "resp_0", 999, 0, 999),
	})
	t.Setenv("CODEX_HOME", dir)

	start := startOfDay(now)
	byModel, available, err := readDay(readCodex, start)
	if err != nil {
		t.Fatalf("readCodex() error = %v", err)
	}
	if !available {
		t.Fatal("readCodex() available = false, want true for an existing sessions dir")
	}
	terra := byModel["gpt-5.6-terra"]
	// Cached input is billed separately, so it is split out of the prompt total.
	if terra.Input != 17000 || terra.CacheRead != 24000 || terra.Output != 500 {
		t.Fatalf("gpt-5.6-terra tokens = %+v, want the cached part split out", terra)
	}
	if got := byModel["gpt-5.6-sol"].Total(); got != 1010 {
		t.Fatalf("gpt-5.6-sol total = %d, want 1010", got)
	}
}

func TestReadCodexSplitsCacheWritesOutOfTheFreshInput(t *testing.T) {
	now := time.Now()
	line := fmt.Sprintf(`{"timestamp":%q,"type":"token_usage_record","payload":{"response_id":"resp_1",`+
		`"usage":{"input_tokens":1000,"cached_input_tokens":600,"cache_write_input_tokens":300,"output_tokens":10}}}`,
		now.UTC().Format(time.RFC3339Nano))
	t.Setenv("CODEX_HOME", writeCodexRollout(t, "rollout-a.jsonl", []string{codexTurnContext(now, "gpt-6.1-sol"), line}))

	byModel, _, err := readDay(readCodex, startOfDay(now))
	if err != nil {
		t.Fatalf("readCodex() error = %v", err)
	}
	// Newer models bill cache writes above the input rate, so they are their
	// own bucket rather than part of the fresh input.
	if got := byModel["gpt-6.1-sol"]; got.Input != 100 || got.CacheRead != 600 || got.CacheWrite != 300 || got.Prompt() != 1000 {
		t.Fatalf("tokens = %+v, want 100 fresh / 600 cached / 300 written of a 1,000-token prompt", got)
	}
}

func TestReadCodexCountsAResumedThreadOnce(t *testing.T) {
	now := time.Now()
	shared := codexRecordLine(now, "resp_1", 1000, 0, 100)
	dir := writeCodexRollout(t, "rollout-a.jsonl", []string{
		codexTurnContext(now, "gpt-5.6-terra"),
		shared,
	})
	// Resuming forks a new rollout that replays the records already written.
	writeCodexRolloutIn(t, dir, "rollout-b.jsonl", []string{
		codexTurnContext(now, "gpt-5.6-terra"),
		shared,
		codexRecordLine(now, "resp_2", 500, 0, 50),
	})
	t.Setenv("CODEX_HOME", dir)

	start := startOfDay(now)
	byModel, _, err := readDay(readCodex, start)
	if err != nil {
		t.Fatalf("readCodex() error = %v", err)
	}
	if got := byModel["gpt-5.6-terra"].Total(); got != 1650 {
		t.Fatalf("total = %d, want 1650 (the replayed response counted once)", got)
	}
}

func TestReadCodexFallsBackToTokenCountDeltas(t *testing.T) {
	now := time.Now()
	dir := writeCodexRollout(t, "legacy.jsonl", []string{
		codexTurnContext(now, "gpt-5.6-luna"),
		codexCountLine(now, 20000, 5000, 200),
		codexCountLine(now.Add(time.Second), 21000, 19000, 300),
	})
	t.Setenv("CODEX_HOME", dir)

	start := startOfDay(now)
	byModel, _, err := readDay(readCodex, start)
	if err != nil {
		t.Fatalf("readCodex() error = %v", err)
	}
	luna := byModel["gpt-5.6-luna"]
	if luna.Input != 17000 || luna.CacheRead != 24000 || luna.Output != 500 {
		t.Fatalf("gpt-5.6-luna tokens = %+v, want both deltas summed", luna)
	}
}

func TestReadCodexPrefersRecordsOverTheDuplicateTokenCounts(t *testing.T) {
	now := time.Now()
	// A version that writes both reports the same request twice; adding them
	// would double the day.
	dir := writeCodexRollout(t, "both.jsonl", []string{
		codexTurnContext(now, "gpt-5.6-terra"),
		codexRecordLine(now, "resp_1", 1000, 0, 100),
		codexCountLine(now.Add(time.Millisecond), 1000, 0, 100),
	})
	t.Setenv("CODEX_HOME", dir)

	start := startOfDay(now)
	byModel, _, err := readDay(readCodex, start)
	if err != nil {
		t.Fatalf("readCodex() error = %v", err)
	}
	if got := byModel["gpt-5.6-terra"].Total(); got != 1100 {
		t.Fatalf("total = %d, want 1100 (the request counted once)", got)
	}
}

func TestReadCodexReportsUnavailableWithoutASessionsDir(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	start := startOfDay(time.Now())
	_, available, err := readDay(readCodex, start)
	if err != nil {
		t.Fatalf("readCodex() error = %v", err)
	}
	if available {
		t.Fatal("readCodex() available = true, want false when Codex has never run here")
	}
}

func writeCodexRollout(t *testing.T, name string, lines []string) string {
	t.Helper()
	root := t.TempDir()
	writeCodexRolloutIn(t, root, name, lines)
	return root
}

func writeCodexRolloutIn(t *testing.T, root, name string, lines []string) {
	t.Helper()
	day := time.Now()
	dir := filepath.Join(root, "sessions", day.Format("2006"), day.Format("01"), day.Format("02"))
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
