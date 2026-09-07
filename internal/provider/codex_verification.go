package provider

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/wavever/CCLimitPing/internal/auth"
	"github.com/wavever/CCLimitPing/internal/codexstate"
	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

const quotaReadBudget = 3 * time.Second

// PingReservation lets the watcher gate and reserve a send using fresh quota data.
// It returns a state claim, which the common ping path finishes after the CLI exits.
type PingReservation func(codexstate.Store, string, string, *usage.Usage) (string, error)

type noQuotaStateKey struct{}
type pingStageKey struct{}

// WithPingStage supplies optional live CLI progress without coupling providers
// to terminal output. The callback must be safe to call from a worker goroutine.
func WithPingStage(ctx context.Context, f func(string)) context.Context {
	return context.WithValue(ctx, pingStageKey{}, f)
}
func pingStage(ctx context.Context, stage string) {
	if f, ok := ctx.Value(pingStageKey{}).(func(string)); ok {
		f(stage)
	}
}

// WithoutQuotaState preserves watch --dry-run's read-only storage behavior.
func WithoutQuotaState(ctx context.Context) context.Context {
	return context.WithValue(ctx, noQuotaStateKey{}, true)
}

func quotaStore() (codexstate.Store, error) {
	dir, err := config.Dir()
	return codexstate.Store{Dir: filepath.Join(dir, "state", "codex")}, err
}

func quotaBucket(name string, cfg config.ProviderConfig) string {
	if name == "spark" {
		return "spark:" + normalizeCodexLimitName(cfg.Model)
	}
	return "codex"
}

func currentCodexAccount(ctx context.Context) (string, error) {
	a := auth.NewCodexAuth()
	id, err := a.AccountID(ctx)
	if err == nil && id == "" {
		err = errors.New("Codex account identity unavailable")
	}
	return id, err
}

func unknownVerification(warning string) *usage.Verification {
	return &usage.Verification{FiveHour: usage.StartStatus{State: codexstate.Unknown},
		Weekly: usage.StartStatus{State: codexstate.Unknown}, Recovery: "verifying", Warning: warning}
}

// Each observation owns its credential snapshot; a watcher cannot retain a
// previous login indefinitely. Authentication retries update that same snapshot.
func readVerifiedUsage(ctx context.Context, name string, cfg config.ProviderConfig, details bool) (*usage.Usage, string, error) {
	a := auth.NewCodexAuth()
	body, r, err := readCodexUsage(ctx, a)
	if err != nil {
		return nil, "", err
	}
	account, err := a.AccountID(ctx)
	if err != nil {
		return nil, "", err
	}
	rl := r.RateLimit
	if name == "spark" {
		rl, err = sparkRateLimitFromResponse(r, cfg.Model)
		if err != nil {
			return nil, account, err
		}
	}
	u := codexUsageToUsage(name, body, r, rl)
	u.QuotaAccount = account
	if r.ResetCredits != nil {
		u.ResetCredits = &usage.ResetCredits{AvailableCount: r.ResetCredits.AvailableCount}
	}
	store, err := quotaStore()
	if ctx.Value(noQuotaStateKey{}) == true {
		u.Verification = unknownVerification("")
	} else if err == nil {
		u.Verification, err = store.Observe(account, quotaBucket(name, cfg), u)
	}
	if err != nil {
		u.Verification = unknownVerification("quota state unavailable: " + err.Error())
	}
	if details {
		if credits, err := readCodexResetCredits(ctx, a); err == nil {
			if id, _ := a.AccountID(ctx); id == account {
				u.ResetCredits = credits
			}
		}
	}
	return u, account, nil
}

func boundedQuota(ctx context.Context, name string, cfg config.ProviderConfig) (*usage.Usage, string, error) {
	readCtx, cancel := context.WithTimeout(ctx, quotaReadBudget)
	defer cancel()
	return readVerifiedUsage(readCtx, name, cfg, false)
}

func pingVerified(ctx context.Context, name string, cfg config.ProviderConfig, dry bool, reserve PingReservation) (*TriggerResult, error) {
	if dry {
		return triggerCodex(ctx, cfg, true)
	}
	pingStage(ctx, "checking quota before ping")
	pre, account, readErr := boundedQuota(ctx, name, cfg)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if readErr != nil {
		return nil, fmt.Errorf("ping not sent: quota precheck failed: %w", readErr)
	}
	warning := pre.Verification.Warning
	identity, identityErr := currentCodexAccount(ctx)
	if identityErr != nil {
		return nil, fmt.Errorf("ping not sent: account check failed: %w", identityErr)
	}
	if identity != account {
		return nil, fmt.Errorf("ping not sent: Codex account changed during quota precheck; retry")
	}
	store, storeErr := quotaStore()
	key := quotaBucket(name, cfg)
	claim := ""
	if storeErr == nil {
		if reserve != nil {
			claim, storeErr = reserve(store, account, key, pre)
		} else {
			claim, storeErr = store.Begin(account, key, false, pre.FetchedAt, time.Now())
		}
	}
	if storeErr != nil {
		if reserve != nil || errors.Is(storeErr, codexstate.ErrBusy) || errors.Is(storeErr, codexstate.ErrDeferred) {
			return nil, fmt.Errorf("ping not sent: %w", storeErr)
		}
		warning = "quota coordination unavailable: " + storeErr.Error()
	}
	// The PTY deadline and claim deadline must describe the same bounded operation.
	pingStage(ctx, "sending ping")
	triggerCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	res, triggerErr := triggerCodex(triggerCtx, cfg, false)
	cancel()
	if res == nil {
		res = &TriggerResult{}
	}
	res.StatusEnabled = cfg.Enabled
	res.PreVerification = pre.Verification
	after, afterErr := currentCodexAccount(ctx)
	changed := account == "" || afterErr != nil || after != account
	if claim != "" {
		if err := store.Finish(account, key, claim, time.Now(), changed); err != nil {
			warning = "quota state update failed: " + err.Error()
		}
	}
	res.Verification = unknownVerification(warning)
	if ctx.Err() == nil {
		pingStage(ctx, "checking quota after ping")
		post, postAccount, err := boundedQuota(ctx, name, cfg)
		if err != nil {
			res.PostcheckErr = err
			res.Verification.Warning = "post-ping quota read failed: " + err.Error()
		} else if !changed && postAccount == account {
			res.Verification = post.Verification
			if warning != "" {
				res.Verification.Warning = warning
			}
		} else {
			res.Verification.Warning = "account changed; ping result cannot be attributed to this quota"
		}
	}
	return res, triggerErr
}
