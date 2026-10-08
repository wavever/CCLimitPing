// Package provider implements per-provider usage reading (zero-quota, via the
// OAuth usage endpoints) and window triggering (via the official CLIs).
package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wavever/CCLimitPing/internal/usage"
)

const (
	usageGETAttempts = 3
	usageGETBackoff  = 500 * time.Millisecond
)

var usageHTTPClient = newUsageHTTPClient()

func newUsageHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultClient
	}
	t := transport.Clone()
	t.ForceAttemptHTTP2 = false
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	} else {
		t.TLSClientConfig = t.TLSClientConfig.Clone()
	}
	t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	return &http.Client{Transport: t}
}

// Provider abstracts a single AI coding provider.
type Provider interface {
	// Name is the stable identifier ("claude", "codex").
	Name() string
	// ReadUsage fetches the current rate-limit snapshot. This is a read-only
	// call against the provider's usage endpoint and consumes no quota.
	ReadUsage(ctx context.Context) (*usage.Usage, error)
	// Trigger sends a minimal message via the official CLI to start a new
	// window. When dryRun is true it fills only Command and executes nothing.
	Trigger(ctx context.Context, dryRun bool) (*TriggerResult, error)
}

// ActiveTaskDetector is optionally implemented by providers that can tell
// whether a user-owned local task is already running and likely to start the
// next window itself.
type ActiveTaskDetector interface {
	ActiveTask(ctx context.Context) (description string, active bool, err error)
}

// Backend outcomes of a reset-credit redemption.
const (
	RedeemReset           = "reset"            // a credit was spent and the eligible windows were reset
	RedeemNothingToReset  = "nothing_to_reset" // no window is eligible; no credit was spent
	RedeemNoCredit        = "no_credit"        // the account has no banked credits
	RedeemAlreadyRedeemed = "already_redeemed" // this attempt already completed
	RedeemCooldown        = "cooldown"         // another reset just went through; nothing was spent
	RedeemIneligible      = "ineligible"       // the credit can no longer be used; nothing was spent
)

// RedeemResult is the backend's definite answer to a redemption.
type RedeemResult struct {
	// Outcome is one of the Redeem* constants, or a code this version does not
	// know (Codex) passed through as is. Empty means nothing was attempted.
	Outcome string
	// Reason is the backend's own reason for the outcome, when it gives one
	// (Claude: "surface", "cli_version", "paused", …).
	Reason string
	// RetryAt is when the backend accepts the next reset, when it said.
	RetryAt time.Time
}

// String renders r for a log line: the outcome, and the reason when given.
func (r RedeemResult) String() string {
	if r.Reason == "" || r.Reason == r.Outcome {
		return r.Outcome
	}
	return r.Outcome + " (" + r.Reason + ")"
}

// ResetCreditRedeemer is optionally implemented by providers that can spend a
// banked rate-limit reset credit. Redeeming is irreversible, so it never
// happens as a side effect of reading usage.
//
// A redemption whose outcome is unknown — the response was lost, or the
// backend could not confirm it — is an error, and the provider remembers its
// request (on disk, shared by every limitping process), so whichever call next
// tries the same reset repeats that request instead of spending a second one.
type ResetCreditRedeemer interface {
	// RedeemResetCredit spends credit — one ReadUsage reported as redeemable —
	// unconditionally and returns the backend's answer. A provider whose
	// backend picks the credit itself (Codex) ignores which one is passed.
	RedeemResetCredit(ctx context.Context, credit usage.ResetCredit) (RedeemResult, error)
	// AutoRedeemResetCredit spends one only when u shows a credit about to
	// lapse, returning an empty Outcome when nothing was attempted. It is
	// throttled internally, so a polling caller may call it every cycle.
	AutoRedeemResetCredit(ctx context.Context, u *usage.Usage) (RedeemResult, error)
}

// ResetCreditReader is implemented by providers whose reset credits are too
// costly to read on every poll (Claude's need a request presenting as Claude
// Code), so ReadUsage leaves them out. Commands the user runs to see or spend
// them read usage through this instead.
type ResetCreditReader interface {
	// ReadUsageWithResetCredits is ReadUsage with the reset credits filled in
	// when they can be read; when they cannot, Usage.ResetCreditsError says why.
	ReadUsageWithResetCredits(ctx context.Context) (*usage.Usage, error)
}

// AutoRedeemReader is implemented by the same providers as ResetCreditReader.
// A polling loop that spends resets on its own (auto_redeem) reads usage
// through it, and only such a loop: the reset credits then ride along on the
// provider's own, sparse cadence, instead of costing every one-shot command
// that happens to read usage a request of their own.
type AutoRedeemReader interface {
	ReadUsageForAutoRedeem(ctx context.Context) (*usage.Usage, error)
}

// ReadUsageForLoop is how a polling loop reads p: through ReadUsageForAutoRedeem
// when the loop will auto-redeem and p offers it, else plainly.
func ReadUsageForLoop(ctx context.Context, p Provider, autoRedeem bool) (*usage.Usage, error) {
	if r, ok := p.(AutoRedeemReader); ok && autoRedeem {
		return r.ReadUsageForAutoRedeem(ctx)
	}
	return p.ReadUsage(ctx)
}

// RedeemHTTPError is a redemption request the backend answered with an HTTP
// error rather than an outcome.
type RedeemHTTPError struct {
	StatusCode int
	Body       string
}

func (e *RedeemHTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("the redeem request returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("the redeem request returned HTTP %d: %s", e.StatusCode, e.Body)
}

// Refused reports whether the backend refused the request as such — a client
// error — so the same request would only be refused again and nothing was
// spent. Timeout, conflict, too-early and rate-limit answers are about the
// moment rather than the request, and leave the outcome open like a 5xx does.
func (e *RedeemHTTPError) Refused() bool {
	switch e.StatusCode {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
		return false
	}
	return e.StatusCode >= 400 && e.StatusCode < 500
}

// asRedeemHTTPError restates a failed redemption request's UsageHTTPError —
// fetchWithAuth's error for every endpoint — as the redeem error it is.
func asRedeemHTTPError(err error) error {
	var httpErr *UsageHTTPError
	if errors.As(err, &httpErr) {
		return &RedeemHTTPError{StatusCode: httpErr.StatusCode, Body: httpErr.Body}
	}
	return err
}

// TriggerResult reports what a Trigger did, including the token usage the ping
// consumed (parsed from the CLI's machine-readable output). CostUSD is 0 when
// the provider doesn't report a cost (e.g. Codex).
type TriggerResult struct {
	Command string
	// Model is the model the ping actually runs on, resolved past an unset
	// config to whatever the provider's own CLI would pick. Empty when that
	// cannot be determined locally. It exists because the command line only
	// names the model when limitping passes one explicitly.
	Model        string
	HasUsage     bool
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	CostUSD      float64
}

// UsageHTTPError preserves usage endpoint HTTP failures so callers can make
// status-aware scheduling decisions instead of treating every failure alike.
type UsageHTTPError struct {
	StatusCode int
	Body       string
	RetryAfter time.Time
}

func (e *UsageHTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("usage endpoint returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("usage endpoint returned HTTP %d: %s", e.StatusCode, e.Body)
}

// tokenSource is satisfied by the auth holders for both providers.
type tokenSource interface {
	Token(ctx context.Context) (string, error)
	Reload(ctx context.Context) (string, error)
	Refresh(ctx context.Context) (string, error)
}

// fetchWithAuth issues a GET built by buildReq using a token from src. On a 401
// it first reloads the credential store (the official CLI may have refreshed
// it) and, failing that, performs an OAuth refresh — each retried once. It
// returns the response body on success.
func fetchWithAuth(ctx context.Context, src tokenSource, buildReq func(token string) (*http.Request, error)) ([]byte, error) {
	token, err := src.Token(ctx)
	if err != nil {
		return nil, err
	}

	body, status, header, err := doGet(ctx, token, buildReq)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		if t, rerr := src.Reload(ctx); rerr == nil && t != token {
			token = t
			if body, status, header, err = doGet(ctx, token, buildReq); err != nil {
				return nil, err
			}
		}
	}
	if status == http.StatusUnauthorized {
		t, rerr := src.Refresh(ctx)
		if rerr != nil {
			return nil, fmt.Errorf("unauthorized and refresh failed: %w", rerr)
		}
		token = t
		if body, status, header, err = doGet(ctx, token, buildReq); err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, &UsageHTTPError{
			StatusCode: status,
			Body:       truncate(body, 300),
			RetryAfter: retryAfterFromHeader(header, time.Now()),
		}
	}
	return body, nil
}

func doGet(ctx context.Context, token string, buildReq func(token string) (*http.Request, error)) ([]byte, int, http.Header, error) {
	var lastBody []byte
	var lastStatus int
	var lastHeader http.Header
	var lastErr error

	for attempt := 1; attempt <= usageGETAttempts; attempt++ {
		req, err := buildReq(token)
		if err != nil {
			return nil, 0, nil, err
		}
		resp, err := usageHTTPClient.Do(req)
		if err != nil {
			lastErr = err
			if !shouldRetryUsageGET(ctx, attempt, 0, err) {
				return nil, 0, nil, err
			}
			if !sleepBeforeUsageRetry(ctx, attempt) {
				return nil, 0, nil, ctx.Err()
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		header := resp.Header.Clone()
		lastBody, lastStatus, lastHeader, lastErr = body, resp.StatusCode, header, readErr

		if readErr != nil {
			if !shouldRetryUsageGET(ctx, attempt, resp.StatusCode, readErr) {
				return nil, resp.StatusCode, header, readErr
			}
			if !sleepBeforeUsageRetry(ctx, attempt) {
				return nil, 0, nil, ctx.Err()
			}
			continue
		}
		if !retryableHTTPStatus(resp.StatusCode) || !shouldRetryUsageGET(ctx, attempt, resp.StatusCode, nil) {
			return body, resp.StatusCode, header, nil
		}
		if !sleepBeforeUsageRetry(ctx, attempt) {
			return nil, 0, nil, ctx.Err()
		}
	}

	return lastBody, lastStatus, lastHeader, lastErr
}

func shouldRetryUsageGET(ctx context.Context, attempt, status int, err error) bool {
	if attempt >= usageGETAttempts || ctx.Err() != nil {
		return false
	}
	if err != nil {
		return transientNetError(err)
	}
	return retryableHTTPStatus(status)
}

func transientNetError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}

func retryableHTTPStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return status >= 500 && status != http.StatusNotImplemented
	}
}

func retryAfterFromHeader(header http.Header, now time.Time) time.Time {
	raw := strings.TrimSpace(header.Get("Retry-After"))
	if raw == "" {
		return time.Time{}
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil && seconds >= 0 {
		return now.Add(time.Duration(seconds * float64(time.Second)))
	}
	if t, err := http.ParseTime(raw); err == nil {
		return t
	}
	return time.Time{}
}

func sleepBeforeUsageRetry(ctx context.Context, attempt int) bool {
	delay := usageGETBackoff * time.Duration(1<<(attempt-1))
	jitter := time.Duration(rand.Int63n(int64(delay / 4)))
	timer := time.NewTimer(delay + jitter)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// shellJoin renders args for display/logging, quoting any a shell would split.
func shellJoin(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		if a == "" || needsShellQuoting(a) {
			out += fmt.Sprintf("%q", a)
		} else {
			out += a
		}
	}
	return out
}

// needsShellQuoting reports whether a shell would not read s back as one
// literal word: whitespace, quotes and the metacharacters a JSON value carries.
func needsShellQuoting(s string) bool {
	return strings.ContainsAny(s, " \t\n\"'{}$*?;&|<>()`\\")
}
