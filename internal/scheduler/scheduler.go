// Package scheduler runs the watch loop: for each provider it sleeps until the
// 5h window resets, then triggers a minimal ping to start the next window,
// keeping windows back-to-back. It respects the weekly limit and never lets a
// transient error kill the loop. When requested on an interactive terminal, it
// also draws a live status line (heartbeat + per-provider countdowns) beneath
// the scrolling log.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/notify"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

const (
	postPingGrace  = 15 * time.Second // wait after a ping before re-reading usage
	minBackoff     = 30 * time.Second
	maxBackoff     = 10 * time.Minute
	rateLimitPause = 5 * time.Minute
	defaultWindow  = 5 * time.Hour // fallback when the API omits the window length
	readTimeout    = 30 * time.Second
	triggerTimeout = 3 * time.Minute
	activeTaskPoll = time.Minute
)

// Target pairs a provider with its scheduling options.
type Target struct {
	Provider   provider.Provider
	AlignStart time.Time // zero = ping as soon as the window is free
	AutoRedeem bool      // spend a reset credit that is about to lapse
}

// Scheduler drives the watch loops.
type Scheduler struct {
	cfg     config.Config
	targets []Target
	dryRun  bool
	log     *log.Logger
	live    *liveStatus
}

// New builds a scheduler that logs to out. When live is true and out is an
// interactive terminal, a live status line is drawn beneath the scrolling log;
// otherwise log output passes straight through.
func New(cfg config.Config, targets []Target, dryRun, live bool, out io.Writer) *Scheduler {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Provider.Name()
	}
	status := newLiveStatus(out, names, live)
	return &Scheduler{
		cfg:     cfg,
		targets: targets,
		dryRun:  dryRun,
		log:     log.New(status, "", log.LstdFlags),
		live:    status,
	}
}

// Run starts one loop per target and blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	names := make([]string, len(s.targets))
	for i, t := range s.targets {
		names[i] = t.Provider.Name()
	}
	s.log.Printf("watching %v (weekly_threshold=%.2f, reset_buffer=%s, notify=%t, dry_run=%t)",
		names, s.cfg.WeeklyThreshold, s.cfg.ResetBuffer.Duration, s.cfg.Notify, s.dryRun)

	var liveWG sync.WaitGroup
	if s.live.enabled {
		liveWG.Add(1)
		go func() {
			defer liveWG.Done()
			s.live.run(ctx)
		}()
	}

	done := make(chan struct{}, len(s.targets))
	for _, t := range s.targets {
		go func(t Target) {
			s.runTarget(ctx, t)
			done <- struct{}{}
		}(t)
	}
	for range s.targets {
		<-done
	}
	liveWG.Wait() // let the render loop clear its line before the final log
	s.log.Printf("shutting down")
}

func (s *Scheduler) runTarget(ctx context.Context, t Target) {
	if p, ok := t.Provider.(provider.VerifiedTrigger); ok && !s.dryRun {
		s.runVerifiedTarget(ctx, t, p)
		return
	}
	name := t.Provider.Name()
	backoff := minBackoff
	aligned := t.AlignStart.IsZero() // whether the align gate has been passed
	var lastPingAt time.Time

	for {
		if ctx.Err() != nil {
			return
		}

		s.live.set(name, "checking usage…", time.Time{})
		rctx, cancel := context.WithTimeout(ctx, readTimeout)
		if s.dryRun {
			rctx = provider.WithoutQuotaState(rctx)
		}
		u, err := t.Provider.ReadUsage(rctx)
		cancel()
		if err != nil {
			var httpErr *provider.UsageHTTPError
			if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests {
				wait := usageRateLimitWait(httpErr.RetryAfter, time.Now())
				s.log.Printf("[%s] usage endpoint rate limited; pausing reads for %s", name, wait.Round(time.Second))
				s.live.set(name, "usage rate limited", time.Now().Add(wait))
				if !sleepCtx(ctx, wait) {
					return
				}
				backoff = minBackoff
				continue
			}
			s.log.Printf("[%s] read usage failed: %v (retry in %s)", name, err, backoff)
			s.live.set(name, "read failed — retrying", time.Now().Add(backoff))
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = minBackoff

		// A spent credit resets the windows, so this snapshot is stale; re-read
		// before deciding anything from it.
		if s.redeemExpiringCredit(ctx, t, u) {
			continue
		}

		// Respect the weekly limit: if exhausted (and no usable credits), wait
		// for the weekly window to reset instead of pinging.
		if s.weeklyExhausted(u) {
			wait := u.Weekly.Remaining() + s.cfg.ResetBuffer.Duration
			if wait <= 0 {
				wait = time.Minute
			}
			s.log.Printf("[%s] weekly limit exhausted (%.0f%%); sleeping %s until weekly reset",
				name, u.Weekly.UsedPercent, wait.Round(time.Second))
			s.live.set(name, fmt.Sprintf("weekly limit reached (%.0f%%)", u.Weekly.UsedPercent), time.Now().Add(wait))
			s.notify(name+": weekly limit reached", "Skipping pings until weekly reset")
			if !sleepCtx(ctx, wait) {
				return
			}
			continue
		}

		// A provider can stop enforcing the 5h window entirely (OpenAI
		// temporarily removed Codex's on 2026-07-12, leaving only the weekly
		// cap). The running weekly window is already anchored, so the next
		// useful ping is the one that anchors the weekly window right after
		// its reset — pinging every 5h would just burn weekly quota.
		if u.FiveHour.Missing() && u.Weekly.Active() {
			wait := u.Weekly.Remaining() + s.cfg.ResetBuffer.Duration
			s.log.Printf("[%s] no 5h window (weekly-only limits, %.0f%%); next ping at weekly reset %s (in %s)",
				name, u.Weekly.UsedPercent,
				u.Weekly.ResetsAt.Local().Format("15:04:05"), wait.Round(time.Second))
			s.live.set(name, fmt.Sprintf("weekly-only %.0f%% — ping at weekly reset", u.Weekly.UsedPercent), time.Now().Add(wait))
			if !sleepCtx(ctx, wait) {
				return
			}
			continue
		}

		// If the 5h window is still running, wait until it resets, then ping.
		if u.FiveHour.Active() {
			wait := u.FiveHour.Remaining() + s.cfg.ResetBuffer.Duration
			s.log.Printf("[%s] 5h window active (%.0f%%), next ping at %s (in %s)",
				name, u.FiveHour.UsedPercent,
				u.FiveHour.ResetsAt.Local().Format("15:04:05"), wait.Round(time.Second))
			s.live.set(name, fmt.Sprintf("5h window %.0f%% — next ping", u.FiveHour.UsedPercent), time.Now().Add(wait))
			if !sleepCtx(ctx, wait) {
				return
			}
			continue
		}

		// Window is free. Guard against double-pinging if our last ping isn't
		// reflected by the endpoint yet.
		if !lastPingAt.IsZero() {
			est := lastPingAt.Add(windowLen(u.FiveHour))
			if time.Now().Before(est) {
				wait := time.Until(est) + s.cfg.ResetBuffer.Duration
				s.log.Printf("[%s] recent ping not yet visible; waiting %s", name, wait.Round(time.Second))
				s.live.set(name, "awaiting window", time.Now().Add(wait))
				if !sleepCtx(ctx, wait) {
					return
				}
				continue
			}
		}

		// Honor the first-window alignment anchor, once.
		if !aligned {
			if d := time.Until(t.AlignStart); d > 0 {
				s.log.Printf("[%s] waiting for align_start %s (in %s)",
					name, t.AlignStart.Local().Format("15:04:05"), d.Round(time.Second))
				s.live.set(name, "waiting for align_start", t.AlignStart)
				if !sleepCtx(ctx, d) {
					return
				}
			}
			aligned = true
		}

		if desc, active, err := activeProviderTask(ctx, t.Provider); err != nil {
			s.log.Printf("[%s] active task check failed: %v; pinging anyway", name, err)
		} else if active {
			s.log.Printf("[%s] window reset but %s is running; waiting %s for it to start the next window",
				name, desc, activeTaskPoll.Round(time.Second))
			s.live.set(name, desc+" active — deferring ping", time.Now().Add(activeTaskPoll))
			if !sleepCtx(ctx, activeTaskPoll) {
				return
			}
			continue
		}

		// Trigger the window.
		if !s.dryRun {
			s.log.Printf("[%s] window reset — triggering ping now…", name)
		}
		s.live.set(name, "window reset — triggering ping…", time.Time{})
		tctx, tcancel := context.WithTimeout(ctx, triggerTimeout)
		res, err := t.Provider.Trigger(tctx, s.dryRun)
		tcancel()
		if s.dryRun {
			if err != nil {
				s.log.Printf("[%s] dry-run ping failed: %v (retry in %s)", name, err, backoff)
				if !sleepCtx(ctx, backoff) {
					return
				}
				backoff = nextBackoff(backoff)
				continue
			}
			s.log.Printf("[%s] DRY-RUN would ping now: %s", name, res.Command)
			// In dry-run we can't actually start a window, so estimate the next
			// cycle from the configured window length to keep the loop sane.
			// Sleep immediately instead of doing an extra usage read that cannot
			// observe a real newly-started window.
			lastPingAt = time.Now()
			wait := windowLen(u.FiveHour) + s.cfg.ResetBuffer.Duration
			s.live.set(name, "dry-run — next estimated ping", lastPingAt.Add(wait))
			if !sleepCtx(ctx, wait) {
				return
			}
			continue
		}
		if err != nil {
			s.log.Printf("[%s] ping failed: %v (retry in %s)", name, err, backoff)
			s.live.set(name, "ping failed — retrying", time.Now().Add(backoff))
			s.notify(name+": ping failed", err.Error())
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		lastPingAt = time.Now()
		s.log.Printf("[%s] ping sent, new window started%s", name, triggerCost(res))
		s.live.set(name, "ping sent — checking window soon", lastPingAt.Add(postPingGrace))
		s.notify(name+": window started", "New 5h window"+triggerCost(res))

		if !sleepCtx(ctx, postPingGrace) {
			return
		}
	}
}

func (s *Scheduler) weeklyExhausted(u *usage.Usage) bool {
	return u.WeeklyExhausted(s.cfg.WeeklyThreshold)
}

// redeemExpiringCredit spends a banked reset credit that is about to lapse,
// when the target opted in. It reports whether the windows were actually reset;
// a failure is logged and never breaks the loop.
func (s *Scheduler) redeemExpiringCredit(ctx context.Context, t Target, u *usage.Usage) bool {
	redeemer, ok := t.Provider.(provider.ResetCreditRedeemer)
	if !t.AutoRedeem || !ok || s.dryRun {
		return false
	}
	name := t.Provider.Name()
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	outcome, err := redeemer.AutoRedeemResetCredit(rctx, u)
	cancel()
	switch {
	case err != nil:
		s.log.Printf("[%s] reset credit redeem failed: %v", name, err)
	case outcome == provider.RedeemReset:
		s.log.Printf("[%s] redeemed an expiring reset credit; rate-limit windows reset", name)
		s.notify(name+": reset credit redeemed", "An expiring reset credit was spent; the windows are reset")
		return true
	case outcome != "":
		s.log.Printf("[%s] reset credit not spent: %s", name, outcome)
	}
	return false
}

func activeProviderTask(ctx context.Context, p provider.Provider) (string, bool, error) {
	detector, ok := p.(provider.ActiveTaskDetector)
	if !ok {
		return "", false, nil
	}
	return detector.ActiveTask(ctx)
}

func (s *Scheduler) notify(title, msg string) {
	if s.cfg.Notify {
		notify.Notify(title, msg)
	}
}

// triggerCost renders the token/cost tail for logs, e.g.
// " — 32934 tok (in 32792 / out 142), $0.0110".
func triggerCost(res *provider.TriggerResult) string {
	if res == nil || !res.HasUsage {
		return ""
	}
	s := fmt.Sprintf(" — %d tok (in %d / out %d)", res.TotalTokens, res.InputTokens, res.OutputTokens)
	if res.CostUSD > 0 {
		s += fmt.Sprintf(", $%.4f", res.CostUSD)
	}
	return s
}

func windowLen(w usage.Window) time.Duration {
	if w.WindowSeconds > 0 {
		return time.Duration(w.WindowSeconds) * time.Second
	}
	return defaultWindow
}

func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

func usageRateLimitWait(retryAfter time.Time, now time.Time) time.Duration {
	if !retryAfter.IsZero() {
		wait := retryAfter.Sub(now)
		if wait > 0 {
			return wait
		}
	}
	return rateLimitPause
}

// sleepCtx sleeps for d and reports false if the context was cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
