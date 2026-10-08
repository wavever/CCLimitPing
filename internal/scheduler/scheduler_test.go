package scheduler

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

type stubProvider struct {
	mu       sync.Mutex
	usage    *usage.Usage
	readErr  error
	trigErr  error
	active   bool // reported by ActiveTask
	reads    int
	triggers int
}

func (p *stubProvider) Name() string { return "stub" }

func (p *stubProvider) ReadUsage(context.Context) (*usage.Usage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	if p.readErr != nil {
		return nil, p.readErr
	}
	return p.usage, nil
}

func (p *stubProvider) Trigger(context.Context, bool) (*provider.TriggerResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.triggers++
	if p.trigErr != nil {
		return nil, p.trigErr
	}
	return &provider.TriggerResult{Command: "stub trigger"}, nil
}

func (p *stubProvider) ActiveTask(context.Context) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return "stub session", p.active, nil
}

func (p *stubProvider) counts() (reads, triggers int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reads, p.triggers
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Notify = false
	cfg.ResetBuffer = config.Duration{}
	return cfg
}

func waitFor(t *testing.T, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func TestRunTargetSleepsWhileFiveHourWindowActive(t *testing.T) {
	p := &stubProvider{
		usage: &usage.Usage{
			FiveHour: usage.Window{
				UsedPercent: 25,
				ResetsAt:    time.Now().Add(time.Second),
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := New(testConfig(), []Target{{Provider: p}}, false, false, io.Discard)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runTarget(ctx, Target{Provider: p})
	}()

	waitFor(t, 200*time.Millisecond, func() bool {
		reads, _ := p.counts()
		return reads == 1
	})
	time.Sleep(50 * time.Millisecond)
	reads, triggers := p.counts()
	if reads != 1 || triggers != 0 {
		t.Fatalf("active window should sleep without polling/triggering; reads=%d triggers=%d", reads, triggers)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("runTarget did not stop after cancellation")
	}
}

// redeemingStub is a provider that can auto-redeem, and counts the reads that
// would carry its reset credits (ReadUsageForAutoRedeem) apart from plain ones.
type redeemingStub struct {
	stubProvider
	autoReads int
}

func (p *redeemingStub) ReadUsageForAutoRedeem(context.Context) (*usage.Usage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.autoReads++
	return p.usage, nil
}

func (p *redeemingStub) RedeemResetCredit(context.Context, usage.ResetCredit) (provider.RedeemResult, error) {
	return provider.RedeemResult{}, errors.New("not used")
}

func (p *redeemingStub) AutoRedeemResetCredit(context.Context, *usage.Usage) (provider.RedeemResult, error) {
	return provider.RedeemResult{}, nil
}

// Only a target that will actually auto-redeem lets its reads carry the reset
// credits; Claude's cost a request presenting as the Claude CLI.
func TestRunTargetReadsResetCreditsOnlyWhenAutoRedeeming(t *testing.T) {
	cases := []struct {
		name       string
		autoRedeem bool
		dryRun     bool
		wantAuto   bool
	}{
		{"auto_redeem off", false, false, false},
		{"auto_redeem on", true, false, true},
		{"dry run never redeems", true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &redeemingStub{stubProvider: stubProvider{usage: &usage.Usage{
				FiveHour: usage.Window{UsedPercent: 25, ResetsAt: time.Now().Add(time.Second)},
			}}}
			target := Target{Provider: p, AutoRedeem: tc.autoRedeem}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := New(testConfig(), []Target{target}, tc.dryRun, false, io.Discard)
			done := make(chan struct{})
			go func() {
				defer close(done)
				s.runTarget(ctx, target)
			}()
			waitFor(t, 200*time.Millisecond, func() bool {
				p.mu.Lock()
				defer p.mu.Unlock()
				return p.reads+p.autoReads >= 1
			})
			cancel()
			<-done
			p.mu.Lock()
			defer p.mu.Unlock()
			if gotAuto := p.autoReads > 0; gotAuto != tc.wantAuto || (p.reads > 0) == tc.wantAuto {
				t.Fatalf("plain reads %d, auto-redeem reads %d; want auto-redeem reads %t", p.reads, p.autoReads, tc.wantAuto)
			}
		})
	}
}

// A window that only a limitping ping has touched reports 0% used — Codex
// rounds used_percent to whole numbers, so ~20k tokens is 0. The scheduler must
// still see it as running; reading it as "free" made the loop fall through to a
// window estimated from the ping time, walking the schedule forward by a ping's
// latency every cycle.
func TestRunTargetSleepsWhileWindowRunsAtZeroPercent(t *testing.T) {
	p := &stubProvider{
		usage: &usage.Usage{
			FiveHour: usage.Window{
				UsedPercent:   0,
				ResetsAt:      time.Now().Add(time.Second),
				WindowSeconds: 18000,
			},
		},
	}
	stop := runStub(t, Target{Provider: p})
	defer stop()

	reads, triggers := settleAndCount(t, p)
	if reads != 1 || triggers != 0 {
		t.Fatalf("a running window at 0%% should be waited out, not re-pinged; reads=%d triggers=%d", reads, triggers)
	}
}

func TestRunTargetWeeklyOnlySleepsUntilWeeklyReset(t *testing.T) {
	p := &stubProvider{
		usage: &usage.Usage{
			// FiveHour left zero: the provider does not enforce a 5h window
			// (Codex since 2026-07-12). Only the weekly window is running.
			Weekly: usage.Window{
				UsedPercent:   24,
				ResetsAt:      time.Now().Add(time.Second),
				WindowSeconds: 604800,
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := New(testConfig(), []Target{{Provider: p}}, false, false, io.Discard)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runTarget(ctx, Target{Provider: p})
	}()

	waitFor(t, 200*time.Millisecond, func() bool {
		reads, _ := p.counts()
		return reads == 1
	})
	time.Sleep(50 * time.Millisecond)
	reads, triggers := p.counts()
	if reads != 1 || triggers != 0 {
		t.Fatalf("weekly-only regime should sleep until the weekly reset without pinging; reads=%d triggers=%d", reads, triggers)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("runTarget did not stop after cancellation")
	}
}

func TestRunTargetDryRunSleepsAfterEstimatedPing(t *testing.T) {
	p := &stubProvider{
		usage: &usage.Usage{
			FiveHour: usage.Window{WindowSeconds: 1},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := New(testConfig(), []Target{{Provider: p}}, true, false, io.Discard)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runTarget(ctx, Target{Provider: p})
	}()

	waitFor(t, 200*time.Millisecond, func() bool {
		_, triggers := p.counts()
		return triggers == 1
	})
	time.Sleep(50 * time.Millisecond)
	reads, triggers := p.counts()
	if reads != 1 || triggers != 1 {
		t.Fatalf("dry-run should sleep on the estimated window without an immediate second usage read; reads=%d triggers=%d", reads, triggers)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("runTarget did not stop after cancellation")
	}
}

// runStub starts runTarget for p in the background and returns a stop func
// that cancels it and waits for the loop to exit.
func runStub(t *testing.T, target Target) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := New(testConfig(), []Target{target}, false, false, io.Discard)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runTarget(ctx, target)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("runTarget did not stop after cancellation")
		}
	}
}

// settleAndCount waits for the first usage read, gives the loop a beat to act,
// and returns the counters.
func settleAndCount(t *testing.T, p *stubProvider) (reads, triggers int) {
	t.Helper()
	waitFor(t, 500*time.Millisecond, func() bool {
		reads, _ := p.counts()
		return reads >= 1
	})
	time.Sleep(50 * time.Millisecond)
	return p.counts()
}

func TestRunTargetWeeklyExhaustedSleepsUntilReset(t *testing.T) {
	p := &stubProvider{
		usage: &usage.Usage{
			FiveHour: usage.Window{WindowSeconds: 18000},
			Weekly: usage.Window{
				UsedPercent:   100,
				ResetsAt:      time.Now().Add(time.Hour),
				WindowSeconds: 604800,
			},
		},
	}
	stop := runStub(t, Target{Provider: p})
	defer stop()

	reads, triggers := settleAndCount(t, p)
	if reads != 1 || triggers != 0 {
		t.Fatalf("exhausted weekly should sleep without pinging; reads=%d triggers=%d", reads, triggers)
	}
}

func TestRunTargetCreditsBypassWeeklyLimit(t *testing.T) {
	p := &stubProvider{
		usage: &usage.Usage{
			FiveHour: usage.Window{WindowSeconds: 18000},
			Weekly: usage.Window{
				UsedPercent:   100,
				ResetsAt:      time.Now().Add(time.Hour),
				WindowSeconds: 604800,
			},
			Credits: &usage.Credits{HasCredits: true},
		},
	}
	stop := runStub(t, Target{Provider: p})
	defer stop()

	waitFor(t, 500*time.Millisecond, func() bool {
		_, triggers := p.counts()
		return triggers == 1
	})
}

func TestRunTargetPausesReadsWhenUsageRateLimited(t *testing.T) {
	p := &stubProvider{
		readErr: &provider.UsageHTTPError{
			StatusCode: 429,
			RetryAfter: time.Now().Add(time.Hour),
		},
	}
	stop := runStub(t, Target{Provider: p})
	defer stop()

	reads, triggers := settleAndCount(t, p)
	if reads != 1 || triggers != 0 {
		t.Fatalf("429 should pause reads until Retry-After; reads=%d triggers=%d", reads, triggers)
	}
}

func TestRunTargetBacksOffOnReadError(t *testing.T) {
	p := &stubProvider{readErr: errors.New("connection reset")}
	stop := runStub(t, Target{Provider: p})
	defer stop()

	reads, triggers := settleAndCount(t, p)
	if reads != 1 || triggers != 0 {
		t.Fatalf("read error should back off without pinging; reads=%d triggers=%d", reads, triggers)
	}
}

func TestRunTargetHonorsAlignStart(t *testing.T) {
	p := &stubProvider{
		usage: &usage.Usage{FiveHour: usage.Window{WindowSeconds: 18000}},
	}
	// settleAndCount asserts at roughly t+60ms; the align gate is set far enough
	// out that scheduling jitter under `go test -race` can't reach it.
	align := time.Now().Add(time.Second)
	stop := runStub(t, Target{Provider: p, AlignStart: align})
	defer stop()

	reads, triggers := settleAndCount(t, p)
	if reads != 1 || triggers != 0 {
		t.Fatalf("should wait for align_start before pinging; reads=%d triggers=%d", reads, triggers)
	}
	waitFor(t, 3*time.Second, func() bool {
		_, triggers := p.counts()
		return triggers == 1
	})
	if time.Now().Before(align) {
		t.Fatal("pinged before align_start")
	}
}

func TestRunTargetDefersWhileProviderTaskActive(t *testing.T) {
	p := &stubProvider{
		usage:  &usage.Usage{FiveHour: usage.Window{WindowSeconds: 18000}},
		active: true,
	}
	stop := runStub(t, Target{Provider: p})
	defer stop()

	reads, triggers := settleAndCount(t, p)
	if reads != 1 || triggers != 0 {
		t.Fatalf("active session should defer the ping; reads=%d triggers=%d", reads, triggers)
	}
}

func TestRunTargetTriggersWhenWindowFree(t *testing.T) {
	p := &stubProvider{
		usage: &usage.Usage{FiveHour: usage.Window{WindowSeconds: 18000}},
	}
	stop := runStub(t, Target{Provider: p})
	defer stop()

	waitFor(t, 500*time.Millisecond, func() bool {
		_, triggers := p.counts()
		return triggers == 1
	})
	// After a ping the loop waits postPingGrace before re-reading; no
	// immediate double ping.
	time.Sleep(50 * time.Millisecond)
	if _, triggers := p.counts(); triggers != 1 {
		t.Fatalf("triggers = %d, want exactly 1", triggers)
	}
}

func TestRunTargetBacksOffOnTriggerFailure(t *testing.T) {
	p := &stubProvider{
		usage:   &usage.Usage{FiveHour: usage.Window{WindowSeconds: 18000}},
		trigErr: errors.New("cli exploded"),
	}
	stop := runStub(t, Target{Provider: p})
	defer stop()

	waitFor(t, 500*time.Millisecond, func() bool {
		_, triggers := p.counts()
		return triggers == 1
	})
	time.Sleep(50 * time.Millisecond)
	if _, triggers := p.counts(); triggers != 1 {
		t.Fatalf("failed trigger should back off, not hammer; triggers = %d", triggers)
	}
}

func TestNextBackoff(t *testing.T) {
	if got := nextBackoff(minBackoff); got != time.Minute {
		t.Fatalf("nextBackoff(30s) = %v, want 1m", got)
	}
	if got := nextBackoff(maxBackoff); got != maxBackoff {
		t.Fatalf("nextBackoff at cap = %v, want %v", got, maxBackoff)
	}
	if got := nextBackoff(6 * time.Minute); got != maxBackoff {
		t.Fatalf("nextBackoff(6m) = %v, want capped at %v", got, maxBackoff)
	}
}

func TestUsageRateLimitWait(t *testing.T) {
	now := time.Now()
	if got := usageRateLimitWait(now.Add(2*time.Minute), now); got != 2*time.Minute {
		t.Fatalf("wait = %v, want the Retry-After delta", got)
	}
	if got := usageRateLimitWait(now.Add(-time.Minute), now); got != rateLimitPause {
		t.Fatalf("past Retry-After wait = %v, want default pause", got)
	}
	if got := usageRateLimitWait(time.Time{}, now); got != rateLimitPause {
		t.Fatalf("missing Retry-After wait = %v, want default pause", got)
	}
}

func TestPingVisibilityWait(t *testing.T) {
	now := time.Now()
	est := now.Add(5 * time.Hour)

	// While confirming, the wait is short: the point is to re-read until the
	// provider publishes the real reset time, not to sit out a whole window.
	wait, confirming := pingVisibilityWait(est, 10*time.Second, 0, now)
	if !confirming || wait != pingConfirm {
		t.Fatalf("first attempt = (%v, %t), want (%v, true)", wait, confirming, pingConfirm)
	}
	if _, confirming := pingVisibilityWait(est, 10*time.Second, pingConfirmMax-1, now); !confirming {
		t.Fatal("last confirmation attempt should still confirm")
	}

	// Out of attempts: fall back to the estimated window, buffer included.
	wait, confirming = pingVisibilityWait(est, 10*time.Second, pingConfirmMax, now)
	if confirming || wait != 5*time.Hour+10*time.Second {
		t.Fatalf("exhausted attempts = (%v, %t), want (5h10s, false)", wait, confirming)
	}

	// A near-boundary estimate is never stretched to the confirm interval.
	if wait, _ := pingVisibilityWait(now.Add(2*time.Second), 0, 0, now); wait != 2*time.Second {
		t.Fatalf("short estimate = %v, want 2s", wait)
	}
}

func TestWindowLen(t *testing.T) {
	if got := windowLen(usage.Window{WindowSeconds: 18000}); got != 5*time.Hour {
		t.Fatalf("windowLen = %v, want 5h", got)
	}
	if got := windowLen(usage.Window{}); got != defaultWindow {
		t.Fatalf("windowLen fallback = %v, want %v", got, defaultWindow)
	}
}

// The watch log is the only record an unattended run leaves, so a ping has to
// say which model it spent quota on.
func TestTriggerModel(t *testing.T) {
	if got := triggerModel(nil); got != "" {
		t.Fatalf("triggerModel(nil) = %q", got)
	}
	if got := triggerModel(&provider.TriggerResult{}); got != "" {
		t.Fatalf("triggerModel(unresolved) = %q, want empty", got)
	}
	res := &provider.TriggerResult{Model: "gpt-5.6-sol"}
	if got, want := triggerModel(res), " (model: gpt-5.6-sol)"; got != want {
		t.Fatalf("triggerModel = %q, want %q", got, want)
	}
}

func TestTriggerCost(t *testing.T) {
	if got := triggerCost(nil); got != "" {
		t.Fatalf("triggerCost(nil) = %q", got)
	}
	if got := triggerCost(&provider.TriggerResult{}); got != "" {
		t.Fatalf("triggerCost(no usage) = %q", got)
	}
	res := &provider.TriggerResult{HasUsage: true, TotalTokens: 100, InputTokens: 90, OutputTokens: 10, CostUSD: 0.011}
	want := " — 100 tok (in 90 / out 10), $0.0110"
	if got := triggerCost(res); got != want {
		t.Fatalf("triggerCost = %q, want %q", got, want)
	}
}
