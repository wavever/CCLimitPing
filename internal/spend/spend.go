// Package spend reports how many tokens the local Claude Code / Codex CLIs
// consumed over the current day, week and month, and what that would have cost
// at API rates.
//
// The providers' usage endpoints only publish rate-limit percentages — never a
// token count — so the numbers come from the transcripts the CLIs already write
// to disk (~/.claude/projects, ~/.codex/sessions), the same source ccusage and
// CodexBar read. That makes this a local-machine view: work done from another
// machine or from the web app is not in these logs and is not counted.
package spend

import (
	"bufio"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/wavever/CCLimitPing/internal/pricing"
)

// ModelSpend is one model's share of a period.
type ModelSpend struct {
	Model   string
	Tokens  pricing.Tokens
	CostUSD float64
	// Priced is false when the pricing dataset has no rates for this model, in
	// which case its tokens are counted but its cost is not.
	Priced bool
}

// Period is a provider's local token consumption over [Start, End), a run of
// whole local calendar days.
type Period struct {
	Provider string
	// Start is local midnight of the first day covered; End is local midnight
	// of the day after the last.
	Start, End time.Time
	// Available reports whether the provider's transcript directory exists at
	// all. False means this CLI has never run on this machine, which is very
	// different from "it ran and spent nothing".
	Available bool
	Tokens    pricing.Tokens
	CostUSD   float64
	// Priced is false when at least one model that consumed tokens had no
	// published rates, making CostUSD a lower bound.
	Priced bool
	// Models is the per-model breakdown, most tokens first.
	Models []ModelSpend
}

// Empty reports whether the period recorded no tokens at all.
func (p Period) Empty() bool { return p.Tokens.Total() == 0 }

// Summary is a provider's consumption over the calendar periods containing a
// moment: its day, its week (Monday first) and its month.
type Summary struct {
	Today, Week, Month Period
}

// lookupPrice resolves a model's rates. It is a variable so tests can price
// their fixtures without reaching for the live dataset.
var lookupPrice = func(ctx context.Context, model string) (pricing.Price, bool) {
	return pricing.Default().Lookup(ctx, model)
}

// sink receives one billed request: when it was made, the model that served it
// and its tokens.
type sink func(at time.Time, model string, tokens pricing.Tokens)

// Summarize returns provider's consumption over the local day, week and month
// containing at. All three come out of one pass over the transcripts, so the
// day and week cost no reading beyond what the month already does. An unknown
// provider yields unavailable periods rather than an error: reading local
// transcripts is a best-effort extra, never a reason for status to fail.
func Summarize(ctx context.Context, provider string, at time.Time) (Summary, error) {
	day := startOfDay(at)
	week := day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	month := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, day.Location())
	s := Summary{
		Today: Period{Provider: provider, Start: day, End: day.AddDate(0, 0, 1), Priced: true},
		Week:  Period{Provider: provider, Start: week, End: week.AddDate(0, 0, 7), Priced: true},
		Month: Period{Provider: provider, Start: month, End: month.AddDate(0, 1, 0), Priced: true},
	}

	var read func(since time.Time, add sink) (bool, error)
	switch provider {
	case "claude":
		read = readClaude
	case "codex":
		read = readCodex
	default:
		return s, nil
	}

	periods := []*Period{&s.Today, &s.Week, &s.Month}
	byModel := make([]map[string]*ModelSpend, len(periods))
	for i := range byModel {
		byModel[i] = map[string]*ModelSpend{}
	}
	// rates memoizes each model's lookup; a nil entry marks a model with no
	// published rates.
	rates := map[string]*pricing.Price{}
	// A week that began last month reaches back further than the month does.
	since := month
	if week.Before(since) {
		since = week
	}
	available, err := read(since, func(at time.Time, model string, tokens pricing.Tokens) {
		rate, looked := rates[model]
		if !looked {
			if r, ok := lookupPrice(ctx, model); ok {
				rate = &r
			}
			rates[model] = rate
		}
		// Priced request by request: a long-context surcharge depends on each
		// request's own prompt, which a model's total no longer shows.
		var cost float64
		if rate != nil {
			cost = rate.CostOf(tokens)
		}
		for i, p := range periods {
			if at.Before(p.Start) || !at.Before(p.End) {
				continue
			}
			ms := byModel[i][model]
			if ms == nil {
				ms = &ModelSpend{Model: model, Priced: rate != nil}
				byModel[i][model] = ms
			}
			ms.Tokens.Add(tokens)
			ms.CostUSD += cost
		}
	})
	// An unreadable transcript is reported, but whatever was read is still
	// totalled: a partial period beats none at all.
	for i, p := range periods {
		p.Available = available
		p.total(byModel[i])
	}
	return s, err
}

// total sums byModel into p.
func (p *Period) total(byModel map[string]*ModelSpend) {
	for _, ms := range byModel {
		if !ms.Priced && ms.Tokens.Total() > 0 {
			p.Priced = false
		}
		p.CostUSD += ms.CostUSD
		p.Tokens.Add(ms.Tokens)
		p.Models = append(p.Models, *ms)
	}
	sort.Slice(p.Models, func(i, j int) bool {
		if a, b := p.Models[i].Tokens.Total(), p.Models[j].Tokens.Total(); a != b {
			return a > b
		}
		return p.Models[i].Model < p.Models[j].Model
	})
}

func startOfDay(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// stampSince parses an RFC3339 transcript stamp, reporting false unless it
// falls at or after since. An unparseable or missing stamp is excluded: a
// record that cannot be dated cannot be attributed to any period.
func stampSince(stamp string, since time.Time) (time.Time, bool) {
	if stamp == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil || t.Before(since) {
		return time.Time{}, false
	}
	return t, true
}

// transcripts returns the .jsonl files under root that were last written on or
// after notBefore. A transcript is append-only, so one untouched since before
// the period cannot hold any of its records — which is what keeps this cheap on
// a history of hundreds of megabytes.
func transcripts(root string, notBefore time.Time) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable corner of the tree must not lose the rest of it.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.ModTime().Before(notBefore) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, err
}

// maxLine caps how much memory one transcript line may cost. Usage records are
// small; the multi-megabyte lines in these logs are tool output and file
// contents, which carry no usage, so skipping them loses nothing.
const maxLine = 4 << 20

// forEachLine calls fn for every newline-delimited record in path. fn must not
// retain line, which is reused across calls.
func forEachLine(path string, fn func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 256<<10)
	var line []byte
	skipping := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !skipping {
			if len(line)+len(chunk) > maxLine {
				line, skipping = line[:0], true
			} else {
				line = append(line, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue // partial line: keep reading until the newline turns up
		}
		if !skipping && len(line) > 0 {
			fn(line)
		}
		line, skipping = line[:0], false
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
