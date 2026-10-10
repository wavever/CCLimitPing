package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/pricing"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/spend"
	"github.com/wavever/CCLimitPing/internal/usage"
)

// TestMain points the transcript scan at an empty directory for the whole
// suite. status reads the local Claude/Codex history for the day's token spend,
// and without this the tests would read the developer's own — slow, and
// different on every machine.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "limitping-cli-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("CLAUDE_CONFIG_DIR", dir)
	os.Setenv("CODEX_HOME", dir)
	// And limitping's own state, so the hook health check status runs sees no
	// hooks rather than the developer's.
	os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestRunStatusPrintsProgressBeforeReadUsage(t *testing.T) {
	var out bytes.Buffer
	var progress bytes.Buffer

	p := fakeStatusProvider{
		name:  "codex",
		usage: &usage.Usage{Provider: "codex"},
		onRead: func() {
			if !strings.Contains(progress.String(), "Fetching codex usage...\n") {
				t.Fatalf("progress output before ReadUsage = %q, want fetching message", progress.String())
			}
		},
	}

	if err := runStatus(context.Background(), &out, &progress, enText, []provider.Provider{p}, false, false, "used", true); err != nil {
		t.Fatalf("runStatus() error = %v", err)
	}
	if !strings.Contains(out.String(), "codex\n") {
		t.Fatalf("status output = %q, want provider usage", out.String())
	}
}

func TestRunStatusJSON(t *testing.T) {
	var out, progress bytes.Buffer

	resets := time.Now().Add(2 * time.Hour)
	providers := []provider.Provider{
		fakeStatusProvider{
			name: "codex",
			usage: &usage.Usage{
				Provider:  "codex",
				Plan:      "pro",
				FiveHour:  usage.Window{UsedPercent: 42.5, ResetsAt: resets, WindowSeconds: 18000},
				FetchedAt: time.Now(),
			},
		},
		fakeStatusProvider{name: "claude", err: errors.New("boom")},
	}

	err := runStatus(context.Background(), &out, &progress, enText, providers, false, true, "used", true)
	if err == nil {
		t.Fatalf("runStatus() error = nil, want failure for the erroring provider")
	}
	if progress.Len() != 0 {
		t.Fatalf("progress = %q, want no chatter in JSON mode", progress.String())
	}

	var got []statusJSON
	if jsonErr := json.Unmarshal(out.Bytes(), &got); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jsonErr, out.String())
	}
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	if got[0].Provider != "codex" || got[0].Plan != "pro" {
		t.Fatalf("entry[0] = %+v, want codex/pro", got[0])
	}
	if got[0].FiveHour == nil || got[0].FiveHour.UsedPercent != 42.5 || !got[0].FiveHour.Active {
		t.Fatalf("entry[0].five_hour = %+v, want 42.5%% active", got[0].FiveHour)
	}
	if got[0].FiveHour.RemainingPercent != 57.5 {
		t.Fatalf("entry[0].five_hour.remaining_percent = %v, want 57.5", got[0].FiveHour.RemainingPercent)
	}
	if got[0].FiveHour.RemainingSeconds <= 0 {
		t.Fatalf("entry[0].five_hour.remaining_seconds = %d, want > 0", got[0].FiveHour.RemainingSeconds)
	}
	if got[0].Weekly != nil {
		t.Fatalf("entry[0].weekly = %+v, want omitted for a window the provider does not enforce", got[0].Weekly)
	}
	if got[1].Provider != "claude" || got[1].Error == "" {
		t.Fatalf("entry[1] = %+v, want claude with error", got[1])
	}
}

func TestRunStatusLocalizesClaudeSubscriptionAccessError(t *testing.T) {
	var out bytes.Buffer
	p := fakeStatusProvider{name: "claude", err: &provider.ClaudeSubscriptionAccessError{}}

	err := runStatus(context.Background(), &out, io.Discard, zhText, []provider.Provider{p}, false, false, "used", true)
	if err == nil {
		t.Fatal("runStatus() error = nil, want provider failure")
	}
	got := out.String()
	if !strings.Contains(got, "Claude 订阅访问不可用") ||
		!strings.Contains(got, "会员已到期/续费失败") ||
		!strings.Contains(got, "Anthropic API Key") {
		t.Fatalf("localized status output = %q", got)
	}
}

func TestRunStatusJSONPreservesClaudeSubscriptionError(t *testing.T) {
	var out bytes.Buffer
	p := fakeStatusProvider{name: "claude", err: &provider.ClaudeSubscriptionAccessError{}}

	err := runStatus(context.Background(), &out, io.Discard, zhText, []provider.Provider{p}, false, true, "used", true)
	if err == nil {
		t.Fatal("runStatus() error = nil, want provider failure")
	}
	var got []statusJSON
	if jsonErr := json.Unmarshal(out.Bytes(), &got); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jsonErr, out.String())
	}
	want := (&provider.ClaudeSubscriptionAccessError{}).Error()
	if len(got) != 1 || got[0].Provider != "claude" || got[0].Error != want {
		t.Fatalf("JSON status = %+v, want claude error %q", got, want)
	}
}

func TestPrintUsageCanShowRemainingPercent(t *testing.T) {
	var out bytes.Buffer
	u := &usage.Usage{
		Provider: "codex",
		FiveHour: usage.Window{UsedPercent: 1},
		Weekly:   usage.Window{UsedPercent: 15},
	}

	printUsage(&out, enText, u, false, "remaining", nil)

	got := out.String()
	if !strings.Contains(got, "99.0% remaining") || !strings.Contains(got, "85.0% remaining") {
		t.Fatalf("status output = %q, want remaining percentages", got)
	}
}

func TestPrintUsageMarksMissingWindowNotEnforced(t *testing.T) {
	var out bytes.Buffer
	u := &usage.Usage{
		Provider: "codex",
		// No 5h window: Codex weekly-only regime since 2026-07-12.
		Weekly: usage.Window{
			UsedPercent:   24,
			ResetsAt:      time.Now().Add(24 * time.Hour),
			WindowSeconds: 604800,
		},
	}

	printUsage(&out, enText, u, false, "used", nil)

	got := out.String()
	if !strings.Contains(got, "5h     not currently enforced") {
		t.Fatalf("status output = %q, want missing 5h window marked as not enforced", got)
	}
	if !strings.Contains(got, "24.0% used") {
		t.Fatalf("status output = %q, want weekly usage rendered", got)
	}
}

func TestPrintUsageRendersChinese(t *testing.T) {
	var out bytes.Buffer
	u := &usage.Usage{
		Provider: "codex",
		Plan:     "plus",
		// Weekly-only regime: the 5h window is not enforced.
		Weekly: usage.Window{
			UsedPercent:   27,
			ResetsAt:      time.Now().Add(24 * time.Hour),
			WindowSeconds: 604800,
		},
		ResetCredits: &usage.ResetCredits{
			AvailableCount: 1,
			Credits: []usage.ResetCredit{
				{
					Status:    "available",
					GrantedAt: time.Now().Add(-24 * time.Hour),
					ExpiresAt: time.Now().Add(29*24*time.Hour + 2*time.Hour),
				},
			},
		},
	}

	printUsage(&out, zhText, u, false, "used", nil)

	got := out.String()
	for _, want := range []string{
		"5h     当前未生效",
		"周     [",
		"27.0% 已用",
		"后重置 (周",
		"重置券 1 张可用",
		"可用，发放于",
		"有效期至",
		"(剩 29d",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("zh status output = %q, want it to contain %q", got, want)
		}
	}
}

func TestPrintUsageIncludesResetCredits(t *testing.T) {
	var out bytes.Buffer
	u := &usage.Usage{
		Provider: "codex",
		ResetCredits: &usage.ResetCredits{
			AvailableCount: 1,
			Credits: []usage.ResetCredit{
				{
					Status:    "available",
					GrantedAt: time.Now().Add(-24 * time.Hour),
					ExpiresAt: time.Now().Add(29*24*time.Hour + 2*time.Hour),
				},
			},
		},
	}

	printUsage(&out, enText, u, false, "used", nil)

	got := out.String()
	if !strings.Contains(got, "reset credits 1 reset available") || !strings.Contains(got, "available") {
		t.Fatalf("status output = %q, want reset credit summary", got)
	}
	if !strings.Contains(got, "(in 29d") {
		t.Fatalf("status output = %q, want remaining lifetime on the expires part", got)
	}
}

// Reset cards the server withholds are said to be withheld, rather than the
// card line silently missing.
func TestPrintUsageSaysWhyResetCardsAreWithheld(t *testing.T) {
	for reason, want := range map[string]string{
		"cli_version": "reset credits not offered: Anthropic did not recognize limitping as the Claude CLI (cli_version)",
		"tier":        "reset credits not offered to this account (tier)",
	} {
		u := &usage.Usage{Provider: "claude", ResetCredits: &usage.ResetCredits{UnavailableReason: reason}}
		var out bytes.Buffer
		printUsage(&out, enText, u, false, "used", nil)
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status output = %q, want it to contain %q", out.String(), want)
		}
		var zh bytes.Buffer
		printUsage(&zh, zhText, u, false, "used", nil)
		if !strings.Contains(zh.String(), reason) {
			t.Fatalf("zh status output = %q, want the reason", zh.String())
		}
	}
}

func TestPrintUsageVerboseSaysTheCardsCouldNotBeRead(t *testing.T) {
	u := &usage.Usage{Provider: "claude", ResetCreditsError: errors.New("the reset-card request was refused")}
	var quiet, verbose bytes.Buffer
	printUsage(&quiet, enText, u, false, "used", nil)
	printUsage(&verbose, enText, u, true, "used", nil)
	if strings.Contains(quiet.String(), "could not be read") {
		t.Fatalf("status output = %q, want the read failure only under -v", quiet.String())
	}
	if !strings.Contains(verbose.String(), "reset credits could not be read: the reset-card request was refused") {
		t.Fatalf("status -v output = %q, want the read failure", verbose.String())
	}
	j := newStatusJSON(u, false, nil)
	if j.ResetCreditsError == "" {
		t.Fatal("status --json dropped the read failure")
	}
}

func TestPrintUsageDescribesClaudeResetCards(t *testing.T) {
	u := &usage.Usage{
		Provider: "claude",
		ResetCredits: &usage.ResetCredits{
			AvailableCount: 3,
			Credits: []usage.ResetCredit{
				{
					Status:    "available",
					ID:        "opus55-launch",
					Label:     "Opus 5.5 launch",
					Left:      1,
					Total:     1,
					Clears:    []string{usage.ClearsFiveHour, usage.ClearsWeekly},
					ExpiresAt: time.Now().Add(21*24*time.Hour + 2*time.Hour),
				},
				{
					Status:        provider.ClaudeGrantQueued,
					ID:            "loyalty",
					Label:         "Loyalty",
					Left:          2,
					Total:         3,
					Clears:        []string{usage.ClearsFiveHour},
					RequiresLimit: true,
				},
			},
		},
	}

	cases := map[string]struct {
		text cliText
		want []string
	}{
		"en": {enText, []string{
			"reset credits 3 resets available",
			"available, Opus 5.5 launch, resets 5h + weekly, expires",
			"(in 21d",
			"queued behind another card, Loyalty, 2 of 3 left, resets 5h, only at the limit",
		}},
		"zh": {zhText, []string{
			"重置券 3 张可用",
			"可用，Opus 5.5 launch，可重置 5h + 周限额，有效期至",
			"排在另一张之后，Loyalty，剩 2/3 次，可重置 5h，仅在触顶时可用",
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			printUsage(&out, tc.text, u, false, "used", nil)
			got := out.String()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("status output = %q, want it to contain %q", got, want)
				}
			}
			// A single-use card needs no "1 of 1 left".
			if strings.Contains(got, "1 of 1") || strings.Contains(got, "1/1") {
				t.Fatalf("status output = %q, want no count on a single-use card", got)
			}
		})
	}
}

func TestStatusJSONCarriesClaudeResetCardDetails(t *testing.T) {
	u := &usage.Usage{Provider: "claude", ResetCredits: &usage.ResetCredits{
		AvailableCount: 1,
		Credits: []usage.ResetCredit{{
			Status: "available", ID: "opus55-launch", Label: "Opus 5.5 launch",
			Left: 1, Total: 1, Clears: []string{usage.ClearsFiveHour, usage.ClearsWeekly},
		}},
	}}
	raw, err := json.Marshal(newStatusJSON(u, false, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"opus55-launch"`, `"label":"Opus 5.5 launch"`, `"left":1`, `"clears":["five_hour","seven_day"]`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("status JSON = %s, want it to contain %s", raw, want)
		}
	}
	if strings.Contains(string(raw), "requires_limit") {
		t.Fatalf("status JSON = %s, want requires_limit omitted when false", raw)
	}
}

func TestPrintUsageShowsTodaysTokensAndCost(t *testing.T) {
	var out bytes.Buffer
	u := &usage.Usage{Provider: "claude", FiveHour: usage.Window{UsedPercent: 12}}

	printUsage(&out, enText, u, false, "used", &testSummary)

	got := out.String()
	for _, want := range []string{
		"today  1.2M tokens  ≈ $3.45\n",
		"week   20.4M tokens  ≈ $42.50\n",
		"month  81.4M tokens  ≈ $160.00\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "claude-opus-5") {
		t.Fatalf("status output = %q, want the per-model breakdown held back for -v", got)
	}
}

func TestPrintUsageBreaksTodayDownWhenVerbose(t *testing.T) {
	var out bytes.Buffer
	u := &usage.Usage{Provider: "claude"}

	printUsage(&out, enText, u, true, "used", &testSummary)

	got := out.String()
	for _, want := range []string{
		"in 20.0K · cache 1.1M read / 100.0K write · out 5,000",
		"claude-opus-5",
		"1.2M tokens  ≈ $3.45",
		// The model with no published rates is still counted, just not costed.
		"brand-new-model",
		"5,000 tokens\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("verbose status output = %q, want it to contain %q", got, want)
		}
	}
}

func TestPrintUsageOmitsTodayWithoutLocalTranscripts(t *testing.T) {
	var out bytes.Buffer
	u := &usage.Usage{Provider: "claude"}

	// The CLI has never run on this machine: "0 tokens" would claim a quiet day
	// that limitping has no way to know about.
	printUsage(&out, enText, u, false, "used", &spend.Summary{Today: spend.Period{Provider: "claude"}})
	printUsage(&out, enText, u, false, "used", nil)

	if got := out.String(); strings.Contains(got, "today") || strings.Contains(got, "month") {
		t.Fatalf("status output = %q, want no spend lines without local data", got)
	}
}

func TestPrintUsageRendersTodayInChinese(t *testing.T) {
	var out bytes.Buffer
	u := &usage.Usage{Provider: "claude"}

	printUsage(&out, zhText, u, true, "used", &testSummary)

	got := out.String()
	for _, want := range []string{
		"今日   1.2M token  ≈ $3.45",
		"输入 20.0K · 缓存 读 1.1M / 写 100.0K · 输出 5,000",
		"本周   20.4M token  ≈ $42.50",
		"本月   81.4M token  ≈ $160.00",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("zh status output = %q, want it to contain %q", got, want)
		}
	}
}

func TestNewSpendJSONCarriesTheBucketsAndFlagsAPartialCost(t *testing.T) {
	got := newSpendJSON(testDay)
	if got == nil {
		t.Fatal("newSpendJSON() = nil, want the day")
	}
	if got.Start != "2026-09-14" {
		t.Fatalf("start = %q, want the local day", got.Start)
	}
	if got.TotalTokens != 1_225_000 || got.CacheReadTokens != 1_100_000 || got.CacheCreationTokens != 100_000 {
		t.Fatalf("today = %+v, want the buckets kept apart", got)
	}
	if got.CostUSD != 3.45 {
		t.Fatalf("cost_usd = %v, want 3.45", got.CostUSD)
	}
	if got.CostComplete {
		t.Fatal("cost_complete = true, want false while a model has no published rates")
	}
	if len(got.Models) != 2 || got.Models[0].Model != "claude-opus-5" {
		t.Fatalf("models = %+v, want the per-model breakdown", got.Models)
	}
	if newSpendJSON(spend.Period{Provider: "claude"}) != nil {
		t.Fatal("newSpendJSON() returned a period for a provider with no local transcripts")
	}
}

func TestNewStatusJSONReportsTodayWeekAndMonth(t *testing.T) {
	got := newStatusJSON(&usage.Usage{Provider: "claude"}, false, &testSummary)
	if got.Today == nil || got.Week == nil || got.Month == nil {
		t.Fatalf("status = %+v, want today, week and month", got)
	}
	// Scripts written before week and month existed read today's "date".
	if got.Today.Date != "2026-09-14" || got.Week.Date != "" {
		t.Fatalf("dates = %q / %q, want date on today only", got.Today.Date, got.Week.Date)
	}
	if got.Week.Start != "2026-09-14" || got.Month.Start != "2026-09-01" {
		t.Fatalf("starts = %q / %q, want Monday and the 1st", got.Week.Start, got.Month.Start)
	}
	if got.Week.CostUSD != 42.5 || got.Month.TotalTokens != 81_400_000 {
		t.Fatalf("week = %+v, month = %+v, want their own totals", got.Week, got.Month)
	}
	if empty := newStatusJSON(&usage.Usage{Provider: "claude"}, false, nil); empty.Today != nil || empty.Week != nil || empty.Month != nil {
		t.Fatalf("status = %+v, want no spend without a local read", empty)
	}
}

func TestHumanTokensStaysExactWhileItIsReadable(t *testing.T) {
	cases := map[int]string{
		0:             "0",
		9_999:         "9,999",
		10_000:        "10.0K",
		1_250_000:     "1.2M",
		2_500_000_000: "2.50B",
	}
	for n, want := range cases {
		if got := humanTokens(n); got != want {
			t.Fatalf("humanTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFmtUSDKeepsSmallSumsVisible(t *testing.T) {
	if got := fmtUSD(0.0032); got != "0.0032" {
		t.Fatalf("fmtUSD(0.0032) = %q, want four decimals", got)
	}
	if got := fmtUSD(12.345); got != "12.35" {
		t.Fatalf("fmtUSD(12.345) = %q, want cents", got)
	}
}

// testDay is a day of local usage: one priced model and one the pricing dataset
// has never heard of.
var testDay = spend.Period{
	Provider:  "claude",
	Start:     time.Date(2026, 9, 14, 0, 0, 0, 0, time.Local),
	End:       time.Date(2026, 9, 15, 0, 0, 0, 0, time.Local),
	Available: true,
	Tokens:    pricing.Tokens{Input: 20_000, CacheRead: 1_100_000, CacheWrite: 100_000, Output: 5_000},
	CostUSD:   3.45,
	Priced:    false,
	Models: []spend.ModelSpend{
		{
			Model:   "claude-opus-5",
			Tokens:  pricing.Tokens{Input: 15_000, CacheRead: 1_100_000, CacheWrite: 100_000, Output: 4_000},
			CostUSD: 3.45,
			Priced:  true,
		},
		{Model: "brand-new-model", Tokens: pricing.Tokens{Input: 5_000}},
	},
}

// testSummary puts testDay, a Monday, in its week and month.
var testSummary = spend.Summary{
	Today: testDay,
	Week: spend.Period{
		Provider:  "claude",
		Start:     time.Date(2026, 9, 14, 0, 0, 0, 0, time.Local),
		End:       time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local),
		Available: true,
		Tokens:    pricing.Tokens{Input: 300_000, CacheRead: 20_000_000, Output: 100_000},
		CostUSD:   42.5,
		Priced:    true,
	},
	Month: spend.Period{
		Provider:  "claude",
		Start:     time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local),
		End:       time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local),
		Available: true,
		Tokens:    pricing.Tokens{Input: 1_000_000, CacheRead: 80_000_000, Output: 400_000},
		CostUSD:   160,
		Priced:    true,
	},
}

func TestResetCreditLineOmitsRemainingWhenRedeemedOrExpired(t *testing.T) {
	redeemed := usage.ResetCredit{
		Status:     "redeemed",
		ExpiresAt:  time.Now().Add(10 * 24 * time.Hour),
		RedeemedAt: time.Now().Add(-time.Hour),
	}
	if line := resetCreditLine(enText, redeemed); strings.Contains(line, "(in ") {
		t.Fatalf("redeemed credit line = %q, want no remaining lifetime", line)
	}
	expired := usage.ResetCredit{
		Status:    "expired",
		ExpiresAt: time.Now().Add(-time.Hour),
	}
	if line := resetCreditLine(enText, expired); strings.Contains(line, "(in ") {
		t.Fatalf("expired credit line = %q, want no remaining lifetime", line)
	}
}

type fakeStatusProvider struct {
	name   string
	usage  *usage.Usage
	err    error
	onRead func()
}

func (f fakeStatusProvider) Name() string {
	return f.name
}

func (f fakeStatusProvider) ReadUsage(context.Context) (*usage.Usage, error) {
	if f.onRead != nil {
		f.onRead()
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.usage, nil
}

func (f fakeStatusProvider) Trigger(context.Context, bool) (*provider.TriggerResult, error) {
	return &provider.TriggerResult{Command: f.name + " ok"}, nil
}

func TestFmtZoneRendersOffsetNotAbbreviation(t *testing.T) {
	cases := []struct {
		name string
		zone *time.Location
		want string
	}{
		{"whole hours east", time.FixedZone("CST", 8*3600), "UTC+8"},
		{"whole hours west", time.FixedZone("EST", -5*3600), "UTC-5"},
		{"half-hour offset", time.FixedZone("IST", 5*3600+1800), "UTC+5:30"},
		{"utc", time.UTC, "UTC"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fmtZone(time.Date(2026, 7, 31, 12, 0, 0, 0, tc.zone)); got != tc.want {
				t.Fatalf("fmtZone() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFmtWindowAndResetCreditCarryTheZone(t *testing.T) {
	zone := fmtZone(time.Now())
	w := usage.Window{UsedPercent: 45, ResetsAt: time.Now().Add(3 * time.Hour), WindowSeconds: 18000}
	if got := fmtWindow(enText, w, "used"); !strings.Contains(got, zone) {
		t.Fatalf("window line = %q, want the zone %q", got, zone)
	}
	credit := usage.ResetCredit{Status: "available", ExpiresAt: time.Now().Add(10 * 24 * time.Hour)}
	if got := resetCreditLine(enText, credit); !strings.Contains(got, zone) {
		t.Fatalf("credit line = %q, want the zone %q on the expiry", got, zone)
	}
}
