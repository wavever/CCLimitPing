package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/wavever/CCLimitPing/internal/codexstate"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

// runVerifiedTarget is shared by Codex and Spark, never by Claude. A successful
// transport does not advance a quota schedule without observation evidence.
func (s *Scheduler) runVerifiedTarget(ctx context.Context, t Target, p provider.VerifiedTrigger) {
	name := t.Provider.Name()
	var reads, prechecks quotaRetry
	aligned := t.AlignStart.IsZero()
	wait := func(reason string, d time.Duration) bool {
		if d <= 0 {
			d = time.Second
		}
		s.live.set(name, reason, time.Now().Add(d))
		return sleepCtx(ctx, d)
	}
	for ctx.Err() == nil {
		s.live.set(name, "checking quota…", time.Time{})
		rctx, cancel := context.WithTimeout(ctx, readTimeout)
		u, err := t.Provider.ReadUsage(rctx)
		cancel()
		if err != nil {
			d := reads.next(err, time.Now())
			s.log.Printf("[%s] quota read failed: %v (retry in %s)", name, err, d)
			if !wait("quota read failed", d) {
				return
			}
			continue
		}
		reads = quotaRetry{}
		if s.redeemExpiringCredit(ctx, t, u) {
			continue
		}
		if s.weeklyExhausted(u) {
			d := u.Weekly.Remaining()
			if d <= 0 {
				// A stale or missing reset must not turn quota reads into a tight loop.
				d = 5 * time.Minute
			}
			if d > 5*time.Minute {
				d = 5 * time.Minute
			}
			if !wait("weekly limit reached", d) {
				return
			}
			continue
		}
		v := u.Verification
		if v == nil || v.Warning != "" {
			if v != nil {
				s.log.Printf("[%s] %s; automatic ping deferred", name, v.Warning)
			}
			if !wait("quota state unavailable", codexstate.Interval) {
				return
			}
			continue
		}
		if !aligned {
			aligned = true
			if d := time.Until(t.AlignStart); d > 0 {
				if !wait("waiting for align_start", d) {
					return
				}
				continue // re-read after alignment; user activity may have started it
			}
		}
		if v.Recovery == "window_started" {
			prechecks = quotaRetry{}
			// Observe at rollover; the verification interval counts toward the buffer.
			d := time.Until(v.NextEligible)
			if d > 5*time.Minute {
				d = 5 * time.Minute
			}
			if !wait(v.Target+" started", d) {
				return
			}
			continue
		}
		if v.Recovery != "ready" || v.NextEligible.After(time.Now()) {
			d := time.Until(v.NextEligible)
			if d <= 0 || d > codexstate.Interval {
				d = codexstate.Interval
			}
			if !wait(v.Target+" "+v.Recovery, d) {
				return
			}
			continue
		}
		if !v.PreviousReset.IsZero() {
			if d := time.Until(v.PreviousReset.Add(s.cfg.ResetBuffer.Duration)); d > 0 {
				if d > 5*time.Minute {
					d = 5 * time.Minute
				}
				if !wait("waiting for reset_buffer", d) {
					return
				}
				continue // polling and verification count toward the same fixed deadline
			}
		}
		if desc, active, err := activeProviderTask(ctx, t.Provider); err != nil || active {
			if !wait(desc+" active or activity unavailable", activeTaskPoll) {
				return
			}
			continue
		}
		s.live.set(name, "checking and sending ping…", time.Time{})
		res, err := p.TriggerWithReservation(ctx, func(store codexstate.Store, account, key string, pre *usage.Usage) (string, error) {
			if pre.Verification == nil || pre.Verification.Warning != "" {
				return "", fmt.Errorf("quota state unavailable")
			}
			if pre.WeeklyExhausted(s.cfg.WeeklyThreshold) {
				return "", codexstate.ErrDeferred
			}
			if _, active, err := activeProviderTask(ctx, t.Provider); err != nil || active {
				return "", codexstate.ErrDeferred
			}
			store.ResetBuffer = s.cfg.ResetBuffer.Duration
			return store.Begin(account, key, true, pre.FetchedAt, time.Now())
		})
		if errors.Is(err, codexstate.ErrBusy) || errors.Is(err, codexstate.ErrDeferred) {
			prechecks = quotaRetry{}
			if !wait("ping deferred", codexstate.Interval) {
				return
			}
			continue
		}
		if err != nil && res == nil {
			// No trigger occurred: do not manufacture a failed ping-history entry.
			d := prechecks.next(err, time.Now())
			s.log.Printf("[%s] pre-ping check failed: %v; observing again in %s", name, err, d)
			if !wait("pre-ping check unavailable", d) {
				return
			}
			continue
		}
		prechecks = quotaRetry{}
		if err != nil {
			s.log.Printf("[%s] ping failed: %v; verifying quota before retry", name, err)
			s.logCodexStartupHint(name, res, err)
			s.notify(name+": ping failed", "Verifying quota before another attempt")
		} else {
			if res != nil && res.TurnCompleted {
				s.log.Printf("[%s] ping turn completed; checking window%s", name, triggerCost(res))
				s.notify(name+": turn completed", "Quota window start is checked separately")
			} else {
				s.log.Printf("[%s] ping trigger returned; checking window%s", name, triggerCost(res))
				s.notify(name+": CLI trigger returned", "Turn completion is unverified; checking quota separately")
			}
		}
		if res != nil && res.Verification != nil && res.Verification.Warning != "" {
			s.log.Printf("[%s] %s", name, res.Verification.Warning)
		}
		delay := codexstate.Interval
		if res != nil && res.PostcheckErr != nil {
			delay = reads.next(res.PostcheckErr, time.Now())
		}
		if !wait("verifying quota", delay) {
			return
		}
	}
}

func (s *Scheduler) logCodexStartupHint(name string, res *provider.TriggerResult, err error) {
	var completionErr *provider.CodexCompletionError
	if !errors.As(err, &completionErr) || res == nil {
		return
	}
	s.log.Printf("[%s] Hint: Codex may be waiting for a startup confirmation. Run this command in a terminal and check for confirmation dialogs: %s", name, res.Command)
	if home := os.Getenv("CODEX_HOME"); home != "" {
		s.log.Printf("[%s] Use the same CODEX_HOME=%q as this watcher.", name, home)
	} else {
		s.log.Printf("[%s] CODEX_HOME is unset for this watcher (defaults to ~/.codex). Use the same setting.", name)
	}
}

type quotaRetry struct {
	delay time.Duration
	auth  bool
}

func (r *quotaRetry) next(err error, now time.Time) time.Duration {
	var httpErr *provider.UsageHTTPError
	var authErr *provider.AuthenticationError
	auth := errors.As(err, &authErr)
	if errors.As(err, &httpErr) {
		auth = auth || httpErr.StatusCode == 401 || httpErr.StatusCode == 403
		if !httpErr.RetryAfter.IsZero() || httpErr.StatusCode == 429 {
			*r = quotaRetry{}
			return usageRateLimitWait(httpErr.RetryAfter, now)
		}
	}
	if r.delay == 0 || r.auth != auth {
		r.delay = minBackoff
	} else {
		r.delay *= 2
	}
	r.auth = auth
	cap := maxBackoff
	if auth {
		cap = time.Hour
	}
	if r.delay > cap {
		r.delay = cap
	}
	return r.delay
}
