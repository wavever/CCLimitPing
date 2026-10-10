package pricing

import (
	"encoding/json"
	"math"
	"testing"
)

// gpt61Sol is shaped like LiteLLM's entry for an OpenAI model with a
// long-context tier, service-tier variants and all.
const gpt61Sol = `{
	"input_cost_per_token": 2e-06,
	"cache_read_input_token_cost": 1e-07,
	"cache_creation_input_token_cost": 2.5e-06,
	"output_cost_per_token": 1e-05,
	"input_cost_per_token_above_272k_tokens": 4e-06,
	"cache_read_input_token_cost_above_272k_tokens": 2e-07,
	"cache_creation_input_token_cost_above_272k_tokens": 5e-06,
	"output_cost_per_token_above_272k_tokens": 1.5e-05,
	"input_cost_per_token_above_272k_tokens_priority": 8e-06,
	"input_cost_per_token_priority": 4e-06
}`

// claudeOpus is shaped like LiteLLM's entry for an Anthropic model: a one-hour
// cache-write rate and no long-context tier.
const claudeOpus = `{
	"input_cost_per_token": 4e-06,
	"cache_read_input_token_cost": 2e-07,
	"cache_creation_input_token_cost": 5e-06,
	"cache_creation_input_token_cost_above_1hr": 8e-06,
	"output_cost_per_token": 2e-05
}`

func TestParseEntryReadsTheLongContextTier(t *testing.T) {
	p, ok := parseEntry(json.RawMessage(gpt61Sol))
	if !ok {
		t.Fatal("parseEntry() ok = false, want the rates")
	}
	if p.LongContext == nil || p.LongContextAbove != 272_000 {
		t.Fatalf("long context = %+v above %d, want a tier above 272,000", p.LongContext, p.LongContextAbove)
	}
	// The priority-tier variant is a different service, not the long-context rate.
	if p.InputPerToken != 2e-06 || p.LongContext.InputPerToken != 4e-06 || p.LongContext.OutputPerToken != 1.5e-05 {
		t.Fatalf("rates = %+v / %+v, want the standard and above-272K lanes", p, *p.LongContext)
	}
}

func TestCostOfBillsTheWholeLongRequestAtTheLongRate(t *testing.T) {
	p, _ := parseEntry(json.RawMessage(gpt61Sol))

	// Exactly at the threshold is still short context.
	short := Tokens{Input: 72_000, CacheRead: 200_000, Output: 1_000}
	wantShort := 72_000*2e-06 + 200_000*1e-07 + 1_000*1e-05
	if got := p.CostOf(short); !near(got, wantShort) {
		t.Fatalf("CostOf(272,000-token prompt) = %v, want %v", got, wantShort)
	}
	// One token over and every token of the request moves to the long rates,
	// cached ones included — not just the excess.
	long := Tokens{Input: 72_001, CacheRead: 200_000, Output: 1_000}
	wantLong := 72_001*4e-06 + 200_000*2e-07 + 1_000*1.5e-05
	if got := p.CostOf(long); !near(got, wantLong) {
		t.Fatalf("CostOf(272,001-token prompt) = %v, want %v", got, wantLong)
	}
}

func TestCostOfBillsOneHourCacheWritesAtTheirOwnRate(t *testing.T) {
	p, _ := parseEntry(json.RawMessage(claudeOpus))

	tokens := Tokens{CacheWrite: 3_000, CacheWrite1h: 2_000}
	want := 1_000*5e-06 + 2_000*8e-06
	if got := p.CostOf(tokens); !near(got, want) {
		t.Fatalf("CostOf() = %v, want %v (5m and 1h writes priced apart)", got, want)
	}

	// A model whose entry lists no one-hour rate falls back to Anthropic's
	// published multiplier: twice the input rate.
	p.CacheWrite1hPerToken = 0
	if got, want := p.CostOf(Tokens{CacheWrite: 1_000, CacheWrite1h: 1_000}), 1_000*8e-06; !near(got, want) {
		t.Fatalf("CostOf() without a 1h rate = %v, want %v", got, want)
	}
}

func TestTotalCountsOneHourWritesOnce(t *testing.T) {
	if got := (Tokens{Input: 1, CacheWrite: 10, CacheWrite1h: 10, Output: 1}).Total(); got != 12 {
		t.Fatalf("Total() = %d, want 12: CacheWrite1h is part of CacheWrite", got)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }
