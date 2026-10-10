package spend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/pricing"
)

func TestForPricesEachModelAndRanksThem(t *testing.T) {
	now := time.Now()
	dir := writeClaudeTranscript(t, "project-a", "session.jsonl", []string{
		claudeLine(now, "msg_1", "req_1", "priced-model", 1000, 0, 0, 100),
		claudeLine(now, "msg_2", "req_2", "unpriced-model", 10, 0, 0, 1),
	})
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	stubPrices(t, map[string]pricing.Price{
		"priced-model": {InputPerToken: 1e-5, OutputPerToken: 1e-4},
	})

	s, err := Summarize(context.Background(), "claude", now)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	day := s.Today
	if !day.Available || day.Tokens.Total() != 1111 {
		t.Fatalf("day = %+v, want 1111 tokens from an available provider", day)
	}
	if want := 0.02; day.CostUSD < want-1e-9 || day.CostUSD > want+1e-9 {
		t.Fatalf("day.CostUSD = %v, want %v", day.CostUSD, want)
	}
	// A model with no published rates leaves the total a lower bound, and the
	// day says so rather than passing the estimate off as complete.
	if day.Priced {
		t.Fatal("day.Priced = true, want false when a model had no rates")
	}
	if len(day.Models) != 2 || day.Models[0].Model != "priced-model" {
		t.Fatalf("day.Models = %+v, want the heaviest model first", day.Models)
	}
	if day.Models[1].Priced || day.Models[1].CostUSD != 0 {
		t.Fatalf("day.Models[1] = %+v, want the unpriced model counted but not costed", day.Models[1])
	}
}

func TestSummarizeBucketsByCalendarDayWeekAndMonth(t *testing.T) {
	// Thursday 3 September 2026: its week began on Monday 31 August, in the
	// previous month, so the week and the month each hold a day the other lacks.
	at := time.Date(2026, 9, 3, 15, 0, 0, 0, time.Local)
	dir := writeClaudeTranscript(t, "project-a", "session.jsonl", []string{
		claudeLine(time.Date(2026, 9, 3, 9, 0, 0, 0, time.Local), "m1", "r1", "priced-model", 1, 0, 0, 0),
		claudeLine(time.Date(2026, 9, 1, 9, 0, 0, 0, time.Local), "m2", "r2", "priced-model", 10, 0, 0, 0),
		claudeLine(time.Date(2026, 8, 31, 9, 0, 0, 0, time.Local), "m3", "r3", "priced-model", 100, 0, 0, 0),
		claudeLine(time.Date(2026, 8, 30, 23, 0, 0, 0, time.Local), "m4", "r4", "priced-model", 1000, 0, 0, 0),
	})
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	stubPrices(t, map[string]pricing.Price{"priced-model": {InputPerToken: 1}})

	s, err := Summarize(context.Background(), "claude", at)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	for _, c := range []struct {
		name       string
		p          Period
		start, end time.Time
		tokens     int
	}{
		{"today", s.Today, time.Date(2026, 9, 3, 0, 0, 0, 0, time.Local), time.Date(2026, 9, 4, 0, 0, 0, 0, time.Local), 1},
		{"week", s.Week, time.Date(2026, 8, 31, 0, 0, 0, 0, time.Local), time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local), 111},
		{"month", s.Month, time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local), time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local), 11},
	} {
		if !c.p.Start.Equal(c.start) || !c.p.End.Equal(c.end) {
			t.Fatalf("%s spans [%v, %v), want [%v, %v)", c.name, c.p.Start, c.p.End, c.start, c.end)
		}
		if !c.p.Available || c.p.Tokens.Total() != c.tokens || c.p.CostUSD != float64(c.tokens) {
			t.Fatalf("%s = %+v, want %d tokens costing $%d", c.name, c.p, c.tokens, c.tokens)
		}
	}
}

func TestSummarizePricesEachRequestAgainstTheLongContextTier(t *testing.T) {
	now := time.Now()
	dir := writeClaudeTranscript(t, "project-a", "session.jsonl", []string{
		// Two short requests whose prompts only exceed the tier together...
		claudeLine(now, "m1", "r1", "tiered-model", 150, 0, 0, 0),
		claudeLine(now, "m2", "r2", "tiered-model", 150, 0, 0, 0),
		// ...and one long one.
		claudeLine(now, "m3", "r3", "tiered-model", 250, 0, 0, 0),
	})
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	stubPrices(t, map[string]pricing.Price{"tiered-model": {
		InputPerToken:    1,
		LongContext:      &pricing.Price{InputPerToken: 2},
		LongContextAbove: 200,
	}})

	s, err := Summarize(context.Background(), "claude", now)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if want := 150.0 + 150 + 2*250; s.Today.CostUSD != want {
		t.Fatalf("cost = %v, want %v: only the long request pays the long rate", s.Today.CostUSD, want)
	}
}

func TestSummarizeIgnoresAnUnknownProvider(t *testing.T) {
	s, err := Summarize(context.Background(), "gemini", time.Now())
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	for _, p := range []Period{s.Today, s.Week, s.Month} {
		if p.Available || !p.Empty() {
			t.Fatalf("period = %+v, want an unavailable, empty period", p)
		}
	}
}

func TestForEachLineSkipsOverlongLinesAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	// Tool output routinely runs to megabytes; buffering one whole would cost
	// far more than the usage record it is standing between.
	body := "first\n" + strings.Repeat("x", maxLine+1024) + "\nlast\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var got []string
	if err := forEachLine(path, func(line []byte) {
		got = append(got, strings.TrimSpace(string(line[:min(len(line), 16)])))
	}); err != nil {
		t.Fatalf("forEachLine() error = %v", err)
	}
	if len(got) != 2 || got[0] != "first" || got[1] != "last" {
		t.Fatalf("lines = %v, want the short lines either side of the huge one", got)
	}
}

func TestForEachLineReadsATrailingLineWithoutNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte("one\ntwo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var got []string
	if err := forEachLine(path, func(line []byte) { got = append(got, strings.TrimSpace(string(line))) }); err != nil {
		t.Fatalf("forEachLine() error = %v", err)
	}
	if len(got) != 2 || got[1] != "two" {
		t.Fatalf("lines = %v, want the unterminated last line too", got)
	}
}

func TestStampSinceExcludesUndatedAndEarlierRecords(t *testing.T) {
	since := startOfDay(time.Now())
	if _, ok := stampSince("", since); ok {
		t.Fatal("stampSince() accepted a record with no stamp")
	}
	if _, ok := stampSince("not-a-time", since); ok {
		t.Fatal("stampSince() accepted a record it could not date")
	}
	if at, ok := stampSince(since.Add(time.Hour).Format(time.RFC3339), since); !ok || !at.Equal(since.Add(time.Hour)) {
		t.Fatalf("stampSince() = %v, %v, want the record from inside the range", at, ok)
	}
	if _, ok := stampSince(since.Add(-time.Second).Format(time.RFC3339), since); ok {
		t.Fatal("stampSince() accepted yesterday's last record")
	}
}

// readDay runs read over the local day starting at start and totals what it
// reports per model, the way Summarize totals one period.
func readDay(read func(time.Time, sink) (bool, error), start time.Time) (map[string]pricing.Tokens, bool, error) {
	end := start.AddDate(0, 0, 1)
	byModel := map[string]pricing.Tokens{}
	available, err := read(start, func(at time.Time, model string, tokens pricing.Tokens) {
		if at.Before(end) {
			t := byModel[model]
			t.Add(tokens)
			byModel[model] = t
		}
	})
	return byModel, available, err
}

// stubPrices swaps the pricing lookup for a fixed table, keeping the tests off
// the live dataset.
func stubPrices(t *testing.T, table map[string]pricing.Price) {
	t.Helper()
	original := lookupPrice
	lookupPrice = func(_ context.Context, model string) (pricing.Price, bool) {
		p, ok := table[model]
		return p, ok
	}
	t.Cleanup(func() { lookupPrice = original })
}
