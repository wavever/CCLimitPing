package scheduler

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

func TestQuotaRetryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		cap  time.Duration
	}{
		{"unauthorized", &provider.UsageHTTPError{StatusCode: 401}, time.Hour},
		{"forbidden", &provider.UsageHTTPError{StatusCode: 403}, time.Hour},
		{"credentials", &provider.AuthenticationError{Err: errors.New("missing credentials")}, time.Hour},
		{"server", &provider.UsageHTTPError{StatusCode: 503}, 10 * time.Minute},
		{"network", errors.New("connection failed"), 10 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var retry quotaRetry
			want := 30 * time.Second
			for i := 0; i < 12; i++ {
				if got := retry.next(tc.err, time.Now()); got != want {
					t.Fatalf("step %d: %s, want %s", i, got, want)
				}
				want *= 2
				if want > tc.cap {
					want = tc.cap
				}
			}
		})
	}
	now := time.Now()
	var retry quotaRetry
	if got := retry.next(&provider.UsageHTTPError{StatusCode: 429, RetryAfter: now.Add(20 * time.Minute)}, now); got != 20*time.Minute {
		t.Fatal(got)
	}
	if got := retry.next(&provider.UsageHTTPError{StatusCode: 429}, now); got != 5*time.Minute {
		t.Fatal(got)
	}
	if got := retry.next(&provider.UsageHTTPError{StatusCode: 403}, now); got != 30*time.Second {
		t.Fatal(got)
	}
}

type verifiedStub struct {
	stubProvider
	postcheckErr error
}

func (p *verifiedStub) TriggerWithReservation(ctx context.Context, _ provider.PingReservation) (*provider.TriggerResult, error) {
	res, err := p.Trigger(ctx, false)
	if res != nil {
		res.PostcheckErr = p.postcheckErr
	}
	return res, err
}

func TestPostcheckFailureSchedulesQuotaRetry(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status           int
		retryAfter, want time.Duration
	}{
		{"rate-limit-deadline", 429, 20 * time.Minute, 20 * time.Minute},
		{"server-deadline", 503, time.Hour, time.Hour},
		{"rate-limit-fallback", 429, 0, 5 * time.Minute},
		{"permission-backoff", 403, 0, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			err := &provider.UsageHTTPError{StatusCode: tc.status}
			if tc.retryAfter > 0 {
				err.RetryAfter = start.Add(tc.retryAfter)
			}
			p := &verifiedStub{stubProvider: stubProvider{usage: &usage.Usage{
				Verification: &usage.Verification{Target: "weekly", Recovery: "ready"},
			}}, postcheckErr: err}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			s := New(testConfig(), []Target{{Provider: p}}, false, false, io.Discard)
			s.live.enabled = true
			s.runVerifiedTarget(ctx, Target{Provider: p}, p)
			item := s.live.items[p.Name()]
			if item.state != "verifying quota" || item.deadline.Sub(start.Add(tc.want)).Abs() > time.Second {
				t.Fatalf("next read: %+v, want delay %s", item, tc.want)
			}
			if reads, sends := p.counts(); reads != 1 || sends != 1 {
				t.Fatal(reads, sends)
			}
		})
	}
}

func TestVerifiedSchedulerGates(t *testing.T) {
	for _, tc := range []struct {
		name, recovery string
		warn, active   bool
		weekly         float64
		want           int
	}{
		{"unknown", "verifying", false, false, 0, 0},
		{"ready", "ready", false, false, 0, 1},
		{"cooldown", "cooldown", false, false, 0, 0},
		{"storage-error", "ready", true, false, 0, 0},
		{"active-user", "ready", false, true, 0, 0},
		{"weekly-guard", "ready", false, false, 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &usage.Verification{Target: "five_hour", Recovery: tc.recovery}
			if tc.warn {
				v.Warning = "cannot write state"
			}
			p := &verifiedStub{stubProvider: stubProvider{active: tc.active, usage: &usage.Usage{
				Weekly: usage.Window{UsedPercent: tc.weekly, ResetsAt: time.Now().Add(time.Hour)}, Verification: v}}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			s := New(testConfig(), []Target{{Provider: p}}, false, false, io.Discard)
			s.Run(ctx)
			_, n := p.counts()
			if n != tc.want {
				t.Fatalf("triggers=%d", n)
			}
		})
	}
}

func TestVerifiedWeeklyExhaustionPolling(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reset time.Time
		want  time.Duration
	}{
		{"missing-reset", time.Time{}, 5 * time.Minute},
		{"stale-reset", time.Now().Add(-time.Minute), 5 * time.Minute},
		{"near-reset", time.Now().Add(30 * time.Second), 30 * time.Second},
		{"distant-reset", time.Now().Add(time.Hour), 5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &verifiedStub{stubProvider: stubProvider{usage: &usage.Usage{
				Weekly: usage.Window{UsedPercent: 100, ResetsAt: tc.reset},
			}}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			s := New(testConfig(), []Target{{Provider: p}}, false, false, io.Discard)
			s.live.enabled = true
			start := time.Now()
			s.runVerifiedTarget(ctx, Target{Provider: p}, p)
			item := s.live.items[p.Name()]
			if item.state != "weekly limit reached" || item.deadline.Sub(start.Add(tc.want)).Abs() > time.Second {
				t.Fatalf("next read: %+v, want delay %s", item, tc.want)
			}
			if reads, sends := p.counts(); reads != 1 || sends != 0 {
				t.Fatalf("reads=%d sends=%d", reads, sends)
			}
		})
	}
}

func TestVerifiedSchedulerHonorsResetBuffer(t *testing.T) {
	for _, tc := range []struct {
		name          string
		previousReset time.Time
		buffer        time.Duration
		want          int
	}{
		{"known-boundary", time.Now().Add(-time.Minute), 10 * time.Minute, 0},
		{"short-buffer", time.Now().Add(-time.Minute), 35 * time.Second, 1},
		{"unknown-boundary", time.Time{}, 10 * time.Minute, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &verifiedStub{stubProvider: stubProvider{usage: &usage.Usage{Verification: &usage.Verification{
				Target: "weekly", Recovery: "ready", PreviousReset: tc.previousReset,
			}}}}
			cfg := testConfig()
			cfg.ResetBuffer.Duration = tc.buffer
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			New(cfg, []Target{{Provider: p}}, false, false, io.Discard).Run(ctx)
			_, n := p.counts()
			if n != tc.want {
				t.Fatalf("triggers=%d want %d", n, tc.want)
			}
		})
	}
}
