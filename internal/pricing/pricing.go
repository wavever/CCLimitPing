// Package pricing computes a USD cost for token usage using the LiteLLM pricing
// dataset — the same approach CodexBar/ccusage use. Providers like Codex (on a
// ChatGPT subscription) don't return a USD cost, so we derive the equivalent
// API-rate cost from the per-model rates.
package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
)

// litellmURL is the canonical pricing dataset ccusage/CodexBar pull from.
const litellmURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

const cacheTTL = 24 * time.Hour

// Price holds per-token rates (USD).
type Price struct {
	InputPerToken      float64
	CachedReadPerToken float64
	CacheWritePerToken float64
	// CacheWrite1hPerToken prices cache writes held for an hour, which
	// Anthropic bills above the default five-minute TTL. Unset, it is twice the
	// input rate, Anthropic's published multiplier.
	CacheWrite1hPerToken float64
	OutputPerToken       float64
	// LongContext, when set, replaces every rate above for a request whose
	// prompt exceeds LongContextAbove tokens — the whole request, not just the
	// excess, as OpenAI (above 272K) and Anthropic (above 200K) bill it.
	LongContext      *Price
	LongContextAbove int
}

// Tokens is a token count split by billing bucket. Input counts only the
// portion billed at the full input rate — the cached and cache-written parts
// are separate, because Anthropic bills all three differently. Output already
// includes reasoning tokens (LiteLLM bills reasoning as output, so callers must
// not add them again).
type Tokens struct {
	Input      int
	CacheRead  int
	CacheWrite int
	// CacheWrite1h is the part of CacheWrite held for an hour rather than five
	// minutes; it is not counted again in Total.
	CacheWrite1h int
	Output       int
}

// Total is every token in t, whatever it was billed at.
func (t Tokens) Total() int {
	return t.Input + t.CacheRead + t.CacheWrite + t.Output
}

// Prompt is the request's whole input: fresh, cached and cache-written. It is
// what a long-context threshold is measured against.
func (t Tokens) Prompt() int {
	return t.Input + t.CacheRead + t.CacheWrite
}

// Add accumulates o into t.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.CacheWrite1h += o.CacheWrite1h
	t.Output += o.Output
}

// CostOf returns the USD cost of one request's tokens t at p's rates. It must
// be given a single request, not a sum of them: whether the long-context rates
// apply depends on that request's prompt alone. A bucket whose rate the dataset
// leaves unset bills at the plain input rate.
func (p Price) CostOf(t Tokens) float64 {
	if p.LongContext != nil && t.Prompt() > p.LongContextAbove {
		p = *p.LongContext
	}
	cacheRead := p.CachedReadPerToken
	if cacheRead == 0 {
		cacheRead = p.InputPerToken
	}
	cacheWrite := p.CacheWritePerToken
	if cacheWrite == 0 {
		cacheWrite = p.InputPerToken
	}
	cacheWrite1h := p.CacheWrite1hPerToken
	if cacheWrite1h == 0 {
		cacheWrite1h = 2 * p.InputPerToken
	}
	hour := min(max(t.CacheWrite1h, 0), t.CacheWrite)
	return float64(t.Input)*p.InputPerToken +
		float64(t.CacheRead)*cacheRead +
		float64(t.CacheWrite-hour)*cacheWrite +
		float64(hour)*cacheWrite1h +
		float64(t.Output)*p.OutputPerToken
}

// Cost returns the USD cost for a request whose inputTotal includes the cached
// portion.
func (p Price) Cost(inputTotal, cached, output int) float64 {
	nonCached := inputTotal - cached
	if nonCached < 0 {
		nonCached = 0
	}
	return p.CostOf(Tokens{Input: nonCached, CacheRead: cached, Output: output})
}

// longContextKey matches the input rate LiteLLM lists for prompts above a
// threshold, e.g. input_cost_per_token_above_272k_tokens. Variants for other
// service tiers (…_tokens_priority, …_batches) do not match.
var longContextKey = regexp.MustCompile(`^input_cost_per_token_above_(\d+)k_tokens$`)

// parseEntry reads one model's rates out of its LiteLLM entry, including the
// one-hour cache-write rate and the long-context tier when the dataset has
// them. ok is false for an entry without an input rate.
func parseEntry(raw json.RawMessage) (Price, bool) {
	var e map[string]any
	if json.Unmarshal(raw, &e) != nil {
		return Price{}, false
	}
	rate := func(key string) float64 {
		v, _ := e[key].(float64)
		return v
	}
	p := Price{
		InputPerToken:        rate("input_cost_per_token"),
		CachedReadPerToken:   rate("cache_read_input_token_cost"),
		CacheWritePerToken:   rate("cache_creation_input_token_cost"),
		CacheWrite1hPerToken: rate("cache_creation_input_token_cost_above_1hr"),
		OutputPerToken:       rate("output_cost_per_token"),
	}
	if p.InputPerToken <= 0 {
		return Price{}, false
	}

	// A model with several tiers is priced at its lowest: the first step up is
	// the one a coding session's prompts actually reach.
	threshold := 0
	for key := range e {
		m := longContextKey.FindStringSubmatch(key)
		if m == nil {
			continue
		}
		if k, err := strconv.Atoi(m[1]); err == nil && k > 0 && (threshold == 0 || k < threshold) {
			threshold = k
		}
	}
	if threshold == 0 || rate(fmt.Sprintf("input_cost_per_token_above_%dk_tokens", threshold)) <= 0 {
		return p, true
	}
	above := func(key string, standard float64) float64 {
		if v := rate(fmt.Sprintf(key, threshold)); v > 0 {
			return v
		}
		return standard
	}
	p.LongContext = &Price{
		InputPerToken:        above("input_cost_per_token_above_%dk_tokens", p.InputPerToken),
		CachedReadPerToken:   above("cache_read_input_token_cost_above_%dk_tokens", p.CachedReadPerToken),
		CacheWritePerToken:   above("cache_creation_input_token_cost_above_%dk_tokens", p.CacheWritePerToken),
		CacheWrite1hPerToken: above("cache_creation_input_token_cost_above_1hr_above_%dk_tokens", p.CacheWrite1hPerToken),
		OutputPerToken:       above("output_cost_per_token_above_%dk_tokens", p.OutputPerToken),
	}
	p.LongContextAbove = threshold * 1000
	return p, true
}

// Fetcher loads and caches the LiteLLM dataset.
type Fetcher struct {
	mu     sync.Mutex
	raw    map[string]json.RawMessage
	loaded bool
}

var (
	defOnce sync.Once
	def     *Fetcher
)

// Default returns a process-wide shared fetcher.
func Default() *Fetcher {
	defOnce.Do(func() { def = &Fetcher{} })
	return def
}

// Lookup resolves a model name (with Codex alias / date-suffix fallbacks) to its
// price, loading the dataset on first use.
func (f *Fetcher) Lookup(ctx context.Context, model string) (Price, bool) {
	if model == "" {
		return Price{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.loadLocked(ctx); err != nil {
		return Price{}, false
	}
	for _, key := range candidates(model) {
		rm, ok := f.raw[key]
		if !ok {
			continue
		}
		if p, ok := parseEntry(rm); ok {
			return p, true
		}
	}
	return Price{}, false
}

func (f *Fetcher) loadLocked(ctx context.Context) error {
	if f.loaded {
		return nil
	}
	data, err := readData(ctx)
	if err != nil {
		return err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	f.raw = m
	f.loaded = true
	return nil
}

// readData returns the dataset from the on-disk cache when fresh, otherwise
// fetches it and refreshes the cache. A stale cache is used if the fetch fails.
func readData(ctx context.Context) ([]byte, error) {
	path := cachePath()
	if path != "" {
		if fi, err := os.Stat(path); err == nil && time.Since(fi.ModTime()) < cacheTTL {
			if b, err := os.ReadFile(path); err == nil {
				return b, nil
			}
		}
	}
	b, err := fetch(ctx)
	if err != nil {
		if path != "" {
			if sb, rerr := os.ReadFile(path); rerr == nil {
				return sb, nil // stale fallback
			}
		}
		return nil, err
	}
	if path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, b, 0o644)
	}
	return b, nil
}

func fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, litellmURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("litellm pricing: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func cachePath() string {
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "litellm_prices.json")
}

var dateSuffix = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

// candidates returns model keys to try, in order: exact, date-suffix-stripped,
// and the Codex "-codex" alias removed (e.g. gpt-5-codex -> gpt-5).
func candidates(model string) []string {
	out := []string{model}
	if s := dateSuffix.FindString(model); s != "" {
		out = append(out, strings.TrimSuffix(model, s))
	}
	if strings.Contains(model, "-codex") {
		out = append(out, strings.Replace(model, "-codex", "", 1))
	}
	return out
}
