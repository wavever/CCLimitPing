package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/wavever/CCLimitPing/internal/auth"
	"github.com/wavever/CCLimitPing/internal/usage"
)

// Claude reset cards. Anthropic hands out usage-limit resets as "grants" — one
// reset for Pro and Max at a model launch, say — and Claude Code spends them
// from /rate-limit-options. None of this is public API: it is what Claude Code
// 2.1.x sends (the program is called cedar_ember there), checked against the
// live endpoints on 2026-09-30.
//
//   - The usage endpoint reports the grants under "cedar_ember" when asked with
//     ?cedar_ember=1, but only to a caller that identifies as the Claude Code
//     CLI; any other User-Agent reads back ineligible_reason "surface".
//   - POST /api/organizations/{org}/reset_rate_limits spends one reset of a
//     grant. Only the grant the server names as next_grant_id is accepted, and
//     request_id makes a retried claim idempotent. A claim the server could not
//     confirm ("unavailable", or any result Claude Code does not know) may still
//     land, so — as Claude Code does — its request id is kept until a definite
//     answer, and the next claim at the grant repeats it (pendingClaims).
//   - The status block also carries the server's own view of the limits
//     (at_limit, exhausted, cooldown_until, and each grant's percent_used and
//     blocking), which the redeem policy prefers to inferring it from the
//     window readings.
//
// Because that means presenting as the CLI, limitping keeps these requests to
// the minimum: Claude Code asks only at a usage limit or when the user opens
// /rate-limit-options, so limitping asks only when the user runs status or
// redeem, or when a loop that auto-redeems (watch, continue) wants them — at
// most hourly, plus once more right before it claims, so the claim is made on
// the cards as they are rather than as they were up to an hour ago. One-shot
// commands that merely read usage (ping, bg status) never ask. Asking replaces
// a usage read rather than adding one, the regular poll never carries it, and
// a failed card read never fails a usage read.
const (
	claudeResetProgram    = "cedar_ember"
	claudeResetCardsURL   = "https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1"
	claudeResetURLFmt     = "https://api.anthropic.com/api/organizations/%s/reset_rate_limits"
	claudeProfileURL      = "https://api.anthropic.com/api/oauth/profile"
	claudeRedeemCooldown  = 15 * time.Minute
	claudeCardsPollPeriod = time.Hour
)

// Grant states beyond the shared available/redeemed/expired.
const (
	ClaudeGrantQueued  = "queued"  // usable, but the server spends another grant first
	ClaudeGrantPaused  = "paused"  // suspended by the server
	ClaudeGrantPending = "pending" // not usable yet (e.g. before its start date)
)

// claudeGrantIDRE is the id shape Claude Code itself insists on before claiming.
var claudeGrantIDRE = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

type claudeResetStatus struct {
	Eligible         bool               `json:"eligible"`
	IneligibleReason *string            `json:"ineligible_reason"`
	Grants           []claudeResetGrant `json:"grants"`
	NextGrantID      string             `json:"next_grant_id"`
	// The server's own reading of the limits: whether one is hit, and which.
	// Both are optional, hence a pointer and a slice that stays nil when absent.
	AtLimit       *bool    `json:"at_limit"`
	Exhausted     []string `json:"exhausted"`
	CooldownUntil string   `json:"cooldown_until"`
}

type claudeResetGrant struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	ResetsTotal int      `json:"resets_total"`
	ResetsLeft  int      `json:"resets_left"`
	StartsAt    string   `json:"starts_at"`
	EndsAt      string   `json:"ends_at"`
	Clears      []string `json:"clears"`
	Paused      bool     `json:"paused"`
	UsableNow   bool     `json:"usable_now"`
	// A pointer because Claude Code treats a missing value as true: a grant
	// that doesn't say is assumed to work only at the limit.
	UseRequiresLimit *bool `json:"use_requires_limit"`
	// The server's reading of each window the grant clears, as a whole
	// percentage; raw, because Claude Code ignores any value that is not one.
	PercentUsed map[string]json.RawMessage `json:"percent_used"`
	Blocking    []string                   `json:"blocking"`
}

// ReadUsageWithResetCredits reads the windows and the reset cards in a single
// request, so asking for the cards never costs the rate-limited usage endpoint
// an extra call. The card half is the unofficial part: if the server rejects
// that request as such (a 4xx), the windows are read the ordinary way instead,
// and the cards are missing — ResetCreditsError says so, and any cards an
// earlier read of this process found are carried over. Any other failure — a
// rate limit, a 5xx, the network, the deadline — would befall the ordinary
// read just the same, so it is returned rather than doubled.
func (c *Claude) ReadUsageWithResetCredits(ctx context.Context) (*usage.Usage, error) {
	c.cardsMu.Lock()
	c.cardsReadAt = time.Now() // a failed read is throttled like a successful one
	c.cardsMu.Unlock()

	u, status, err := c.readUsage(ctx, claudeResetCardsURL, claudeCLIUserAgent())
	if err != nil {
		var httpErr *UsageHTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode < 400 || httpErr.StatusCode >= 500 ||
			httpErr.StatusCode == http.StatusTooManyRequests || ctx.Err() != nil {
			return nil, err
		}
		u, _, ferr := c.readUsage(ctx, claudeUsageURL, claudeCodeUserAgent())
		if ferr != nil {
			return nil, ferr
		}
		u.ResetCredits = c.cachedResetCredits().WithoutReadings()
		u.ResetCreditsError = fmt.Errorf("the reset-card request was refused: %w", err)
		return u, nil
	}
	if status == nil {
		// Asked for, yet absent: the server no longer reports the cards this
		// way, which is not the same as holding none.
		u.ResetCreditsError = errors.New("the usage response carried no reset-card status")
		return u, nil
	}
	u.ResetCredits = claudeResetCreditsToUsage(status, u.FetchedAt)
	c.cardsMu.Lock()
	c.cards = u.ResetCredits
	c.cardsMu.Unlock()
	return u, nil
}

// ReadUsageForAutoRedeem is the read of a loop that auto-redeems: the plain
// usage read, with the cards from the last card read attached — and, at most
// once per claudeCardsPollPeriod, a card read in its place. A card's expiry,
// which is what the policy waits for, does not change in between.
//
// The cached cards travel without the server's limit verdict and usage
// readings: those are an hour old, while the windows beside them are fresh, so
// the policy judges by the windows — and AutoRedeemResetCredit reads the cards
// afresh before it claims anything.
func (c *Claude) ReadUsageForAutoRedeem(ctx context.Context) (*usage.Usage, error) {
	if c.resetCreditsDue() {
		return c.ReadUsageWithResetCredits(ctx)
	}
	u, _, err := c.readUsage(ctx, claudeUsageURL, claudeCodeUserAgent())
	if err != nil {
		return nil, err
	}
	u.ResetCredits = c.cachedResetCredits().WithoutReadings()
	return u, nil
}

// resetCreditsDue reports whether the auto_redeem poll should read the cards
// again: at most once per claudeCardsPollPeriod.
func (c *Claude) resetCreditsDue() bool {
	c.cardsMu.Lock()
	defer c.cardsMu.Unlock()
	return c.cardsReadAt.IsZero() || time.Since(c.cardsReadAt) >= claudeCardsPollPeriod
}

// cachedResetCredits is the last cards read, reused between reads.
func (c *Claude) cachedResetCredits() *usage.ResetCredits {
	c.cardsMu.Lock()
	defer c.cardsMu.Unlock()
	return c.cards
}

// forgetResetCredits drops the cached cards after a claim, whose outcome may
// have changed them, so the next poll reads them afresh.
func (c *Claude) forgetResetCredits() {
	c.cardsMu.Lock()
	c.cards, c.cardsReadAt = nil, time.Time{}
	c.cardsMu.Unlock()
}

// claudeResetCreditsToUsage maps the grants onto the shared reset model. Each
// grant is one entry however many resets it holds; AvailableCount totals the
// resets the account still holds, which is what Claude Code shows too.
func claudeResetCreditsToUsage(s *claudeResetStatus, now time.Time) *usage.ResetCredits {
	if s == nil {
		return nil
	}
	if !s.Eligible {
		if reason := claudeUnavailableReason(s.IneligibleReason); reason != "" {
			return &usage.ResetCredits{UnavailableReason: reason}
		}
		return nil
	}
	if len(s.Grants) == 0 {
		return nil
	}
	out := &usage.ResetCredits{
		Credits:       make([]usage.ResetCredit, 0, len(s.Grants)),
		LimitState:    s.AtLimit != nil || s.Exhausted != nil,
		AtLimit:       s.AtLimit != nil && *s.AtLimit,
		Exhausted:     claudeClearedWindows(s.Exhausted),
		CooldownUntil: parseTime(s.CooldownUntil),
	}
	for _, g := range s.Grants {
		c := usage.ResetCredit{
			ID:            g.ID,
			Label:         g.Label,
			Left:          g.ResetsLeft,
			Total:         g.ResetsTotal,
			GrantedAt:     parseTime(g.StartsAt),
			ExpiresAt:     parseTime(g.EndsAt),
			Clears:        claudeClearedWindows(g.Clears),
			RequiresLimit: g.UseRequiresLimit == nil || *g.UseRequiresLimit,
			Blocking:      claudeClearedWindows(g.Blocking),
		}
		c.PercentUsed = claudePercentUsed(g.PercentUsed, c)
		c.Status = claudeGrantStatus(g, s.NextGrantID, c.Expired(now))
		if !c.Expired(now) {
			out.AvailableCount += g.ResetsLeft
		}
		out.Credits = append(out.Credits, c)
	}
	return out
}

// claudeUnavailableReason is the ineligible_reason worth telling the user. It
// is empty for the reasons that just mean there is nothing on offer — no grant,
// or the program switched off — since "no reset cards" already says that.
func claudeUnavailableReason(reason *string) string {
	if reason == nil {
		return ""
	}
	switch *reason {
	case "", "no_grant", "config_off":
		return ""
	}
	return *reason
}

// claudePercentUsed keeps the server's reading of each window c clears, under
// the names claudeClearedWindows gives them. Like Claude Code, it ignores any
// value that is not a whole percentage; where two names fold into one, the
// higher reading stands for both.
func claudePercentUsed(raw map[string]json.RawMessage, c usage.ResetCredit) map[string]float64 {
	var out map[string]float64
	for name, v := range raw {
		var n float64
		if json.Unmarshal(v, &n) != nil || n != math.Trunc(n) || n < 0 || n > 100 {
			continue
		}
		name = claudeWindowName(name)
		if !c.ClearsWindow(name) {
			continue
		}
		if out == nil {
			out = map[string]float64{}
		}
		if prev, ok := out[name]; !ok || n > prev {
			out[name] = n
		}
	}
	return out
}

// claudeGrantStatus reduces a grant to one state. Only the server's next grant
// can be "available": claiming any other is refused as not_next_grant.
func claudeGrantStatus(g claudeResetGrant, nextID string, expired bool) string {
	switch {
	case expired:
		return "expired"
	case g.ResetsLeft <= 0:
		return "redeemed"
	case g.Paused:
		return ClaudeGrantPaused
	case !g.UsableNow:
		return ClaudeGrantPending
	case g.ID != nextID:
		return ClaudeGrantQueued
	default:
		return "available"
	}
}

// claudeClearedWindows folds Claude's limit names onto the shared ones. The
// overage-included weekly bucket is the weekly limit as far as a reset goes, as
// Claude Code itself words it; the per-model weekly limits pass through as-is.
// The same folding applies wherever the server names windows: a grant's clears
// and blocking, the status block's exhausted, and percent_used's keys.
func claudeClearedWindows(raw []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range raw {
		w = claudeWindowName(w)
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// claudeWindowName is one window's name after claudeClearedWindows' folding.
func claudeWindowName(w string) string {
	if w == "seven_day_overage_included" {
		return usage.ClearsWeekly
	}
	return w
}

// RedeemResetCredit spends one reset of credit's grant right now. Each call is
// a distinct attempt, so it carries a fresh request id — unless an earlier
// claim at the grant is still in doubt, which it then repeats (see
// pendingClaims), so re-running `redeem` after an unconfirmed claim is safe.
func (c *Claude) RedeemResetCredit(ctx context.Context, credit usage.ResetCredit) (RedeemResult, error) {
	res, _, err := c.claim(ctx, credit, randomIdempotencyKey())
	return res, err
}

// AutoRedeemResetCredit spends a reset whose grant is about to lapse, at most
// once per claudeRedeemCooldown.
//
// u's cards may be up to an hour old (see ReadUsageForAutoRedeem), and another
// limitping process may have spent a reset since, so u only decides whether a
// claim is worth considering. The cards are then read afresh, and the policy
// asked again on what the server says now — its own limit verdict included —
// before anything is claimed. That read counts against the cooldown, so a
// policy that keeps saying yes on stale cards costs at most one card read per
// cooldown.
func (c *Claude) AutoRedeemResetCredit(ctx context.Context, u *usage.Usage) (RedeemResult, error) {
	if _, ok := u.ResetCreditToRedeem(time.Now()); !ok {
		return RedeemResult{}, nil
	}
	c.redeemMu.Lock()
	if time.Since(c.lastRedeem) < claudeRedeemCooldown {
		c.redeemMu.Unlock()
		return RedeemResult{}, nil
	}
	c.lastRedeem = time.Now()
	c.redeemMu.Unlock()

	fresh, err := c.ReadUsageWithResetCredits(ctx)
	if err == nil {
		err = fresh.ResetCreditsError
	}
	if err != nil {
		return RedeemResult{}, fmt.Errorf("claude reset: re-reading the cards before claiming: %w", err)
	}
	credit, ok := fresh.ResetCreditToRedeem(time.Now())
	if !ok {
		return RedeemResult{}, nil
	}
	base := claudeClaimKeyBase(credit)
	res, s, err := c.claim(ctx, credit, c.attempts.key(base))
	c.attempts.settle(base, s)
	return res, err
}

// claudeClaimKeyBase identifies one reset of a grant: the grant and the resets
// it still holds, so the grant's next reset is a different one. Request ids
// derive from it per attempt (see redeemAttempts): a claim whose outcome is
// unknown is retried as the same request and cannot spend a second reset, while
// a definite refusal (not at a limit, cooldown) moves on to a fresh id. A claim
// in doubt recorded on disk (pendingClaims) overrides either.
func claudeClaimKeyBase(c usage.ResetCredit) string {
	return fmt.Sprintf("limitping-claude-reset|%s|%d", c.ID, c.Left)
}

// claudeClaimRequestID is the request id of the first attempt at c.
func claudeClaimRequestID(c usage.ResetCredit) string {
	var first redeemAttempts
	return first.key(claudeClaimKeyBase(c))
}

// claim spends one reset of credit's grant under requestID, or under the
// request id of an earlier claim at the grant that is still in doubt — in
// this process or any other — and records how it ended.
func (c *Claude) claim(ctx context.Context, credit usage.ResetCredit, requestID string) (RedeemResult, claimSettlement, error) {
	if !claudeGrantIDRE.MatchString(credit.ID) {
		return RedeemResult{}, claimNotSent, fmt.Errorf("claude reset: malformed grant id %q", credit.ID)
	}
	org, err := c.organizationUUID(ctx)
	if err != nil {
		return RedeemResult{}, claimNotSent, fmt.Errorf("claude reset: %w", err)
	}
	return pendingClaims{provider: "claude"}.claim(credit.ID, org, credit.Left, requestID,
		func(requestID string) (RedeemResult, claimSettlement, error) {
			return c.postClaim(ctx, org, credit.ID, requestID)
		})
}

type claudeClaimResponse struct {
	Result        string `json:"result"`
	Reason        string `json:"reason"`
	CooldownUntil string `json:"cooldown_until"`
}

// postClaim sends the claim itself.
func (c *Claude) postClaim(ctx context.Context, org, grantID, requestID string) (RedeemResult, claimSettlement, error) {
	payload, err := json.Marshal(map[string]string{
		"program":    claudeResetProgram,
		"grant_id":   grantID,
		"request_id": requestID,
	})
	if err != nil {
		return RedeemResult{}, claimNotSent, err
	}
	endpoint := fmt.Sprintf(claudeResetURLFmt, url.PathEscape(org))
	defer c.forgetResetCredits()
	body, err := fetchWithAuth(ctx, c.auth, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		setClaudeOAuthHeaders(req, token, claudeCLIUserAgent())
		return req, nil
	})
	if err != nil {
		err = asRedeemHTTPError(err)
		s := settlementOf(err)
		if s == claimInDoubt {
			return RedeemResult{}, s, claudeClaimInDoubt(err)
		}
		return RedeemResult{}, s, fmt.Errorf("claude reset: the claim was refused, nothing was spent: %w", err)
	}
	var r claudeClaimResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return RedeemResult{}, claimInDoubt, claudeClaimInDoubt(fmt.Errorf("unreadable response: %w", err))
	}
	return claudeClaimResult(r, body)
}

// claudeClaimResult reads the backend's answer to a claim.
func claudeClaimResult(r claudeClaimResponse, body []byte) (RedeemResult, claimSettlement, error) {
	res := RedeemResult{Reason: r.Reason, RetryAt: parseTime(r.CooldownUntil)}
	switch r.Result {
	case "reset":
		res.Outcome = RedeemReset
	case "not_limited":
		res.Outcome = RedeemNothingToReset
	case "already_used":
		res.Outcome = RedeemAlreadyRedeemed
	case "cooldown":
		res.Outcome = RedeemCooldown
	case "ineligible":
		res.Outcome = RedeemIneligible
	case "unavailable":
		reason := r.Reason
		if reason == "" {
			reason = "no reason given"
		}
		return RedeemResult{}, claimInDoubt, claudeClaimInDoubt(fmt.Errorf("the backend could not confirm it (%s)", reason))
	case "":
		return RedeemResult{}, claimInDoubt, claudeClaimInDoubt(fmt.Errorf("no result in the response: %s", truncate(body, 200)))
	default:
		// Claude Code reads every result it does not know as "unavailable":
		// claiming it as an outcome would let the next attempt move on to a
		// fresh request while this one may have spent the reset.
		return RedeemResult{}, claimInDoubt, claudeClaimInDoubt(fmt.Errorf("unexpected result %q", r.Result))
	}
	return res, claimAnswered, nil
}

// claudeClaimInDoubt reports a claim that may or may not have spent a reset,
// and why it is safe to try again: the retry repeats this same request.
func claudeClaimInDoubt(cause error) error {
	return fmt.Errorf("claude reset: unconfirmed — %w; the reset may still go through, so check 'limitping status' first. Trying again is safe: it repeats this same request, which cannot spend a second reset", cause)
}

// organizationUUID names the organization a claim is posted to. Claude Code
// records it at login in its global config; the profile endpoint answers the
// same question when that file is missing or unreadable.
func (c *Claude) organizationUUID(ctx context.Context) (string, error) {
	if id := claudeConfigOrganizationUUID(); id != "" {
		return id, nil
	}
	body, err := fetchWithAuth(ctx, c.auth, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeProfileURL, nil)
		if err != nil {
			return nil, err
		}
		setClaudeOAuthHeaders(req, token, claudeCLIUserAgent())
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("reading the Claude organization: %w", err)
	}
	var p struct {
		Organization struct {
			UUID string `json:"uuid"`
		} `json:"organization"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", fmt.Errorf("reading the Claude organization: %w", err)
	}
	if p.Organization.UUID == "" {
		return "", errors.New("the Claude login has no organization; run 'claude' and /login again")
	}
	return p.Organization.UUID, nil
}

// claudeConfigOrganizationUUID reads oauthAccount.organizationUuid from Claude
// Code's global config, which lives where Claude Code puts it: under
// CLAUDE_CONFIG_DIR when set, else the home directory.
func claudeConfigOrganizationUUID() string {
	path, err := auth.ClaudeGlobalConfigPath()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg struct {
		OAuthAccount struct {
			OrganizationUUID string `json:"organizationUuid"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return ""
	}
	return cfg.OAuthAccount.OrganizationUUID
}
