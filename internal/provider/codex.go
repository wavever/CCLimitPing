package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/wavever/CCLimitPing/internal/activity"
	"github.com/wavever/CCLimitPing/internal/auth"
	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/pricing"
	"github.com/wavever/CCLimitPing/internal/usage"
)

const (
	codexDefaultBaseURL = "https://chatgpt.com/backend-api"
	codexChatGPTPath    = "/wham/usage"
	codexResetPath      = "/wham/rate-limit-reset-credits"
	codexConsumePath    = "/wham/rate-limit-reset-credits/consume"
	codexAPIPath        = "/api/codex/usage"
	codexUserAgent      = "limitping"

	// codexRedeemCooldown throttles the automatic redemption path so a
	// once-a-minute poll loop cannot re-attempt a refused redemption every cycle.
	codexRedeemCooldown = 15 * time.Minute

	// codexAnchorSkew is how much clock disagreement codexWindowAnchored tolerates
	// when it has to fall back to the local clock. A ping's own window is at least
	// postPingGrace old by the time the scheduler looks, so erring on the side of
	// "not started" costs at most one extra ping, while erring the other way would
	// park watch on a window that does not exist.
	codexAnchorSkew = 5 * time.Second
)

// Codex reads usage via the ChatGPT backend usage endpoint and triggers windows
// with a headless `codex exec --ephemeral` request, so a ping leaves no session
// behind in the Codex thread list.
type Codex struct {
	cfg  config.ProviderConfig
	auth *auth.CodexAuth

	redeemMu   sync.Mutex
	lastRedeem time.Time // last automatic redemption attempt, for the cooldown
}

func NewCodex(cfg config.ProviderConfig) *Codex {
	return &Codex{
		cfg:  cfg,
		auth: auth.NewCodexAuth(),
	}
}

func (c *Codex) Name() string { return "codex" }

func (c *Codex) ActiveTask(ctx context.Context) (string, bool, error) {
	return codexActiveTask(ctx)
}

func (c *Codex) ReadUsage(ctx context.Context) (*usage.Usage, error) {
	body, r, err := readCodexUsage(ctx, c.auth)
	if err != nil {
		return nil, err
	}
	u := codexUsageToUsage(c.Name(), body, r)
	if credits, err := readCodexResetCredits(ctx, c.auth); err == nil {
		u.ResetCredits = credits
	} else if r.ResetCredits != nil {
		// The detail endpoint is private and may go away; the usage response
		// itself now embeds the available count, so keep at least that.
		u.ResetCredits = &usage.ResetCredits{AvailableCount: r.ResetCredits.AvailableCount}
	}
	return u, nil
}

func (c *Codex) Trigger(ctx context.Context, dryRun bool) (*TriggerResult, error) {
	return triggerCodex(ctx, c.cfg, dryRun)
}

// RedeemResetCredit spends the next available reset credit right now. Each call
// is a distinct attempt, so it carries a fresh idempotency key.
func (c *Codex) RedeemResetCredit(ctx context.Context) (string, error) {
	return c.consumeResetCredit(ctx, randomIdempotencyKey())
}

// AutoRedeemResetCredit spends a credit that is about to lapse, at most once per
// codexRedeemCooldown. The key is derived from the credit itself, so an attempt
// whose response was lost in flight is retried — after the cooldown — under the
// same key and cannot spend a second credit.
func (c *Codex) AutoRedeemResetCredit(ctx context.Context, u *usage.Usage) (string, error) {
	credit, ok := u.ResetCreditToRedeem(time.Now())
	if !ok {
		return "", nil
	}
	c.redeemMu.Lock()
	if time.Since(c.lastRedeem) < codexRedeemCooldown {
		c.redeemMu.Unlock()
		return "", nil
	}
	c.lastRedeem = time.Now()
	c.redeemMu.Unlock()
	return c.consumeResetCredit(ctx, creditIdempotencyKey(credit))
}

// consumeResetCredit redeems one banked reset credit. The credit id is
// deliberately omitted: the backend then picks the next available credit — the
// same one the policy targets — so we don't depend on an id field this private
// endpoint doesn't document.
func (c *Codex) consumeResetCredit(ctx context.Context, idempotencyKey string) (string, error) {
	payload, err := json.Marshal(map[string]string{"idempotency_key": idempotencyKey})
	if err != nil {
		return "", err
	}
	accountID, _ := c.auth.AccountID(ctx)
	body, err := fetchWithAuth(ctx, c.auth, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexConsumeURL(), bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", codexUserAgent)
		req.Header.Set("OpenAI-Beta", "codex-1")
		req.Header.Set("originator", "Codex Desktop")
		if accountID != "" {
			req.Header.Set("ChatGPT-Account-Id", accountID)
		}
		return req, nil
	})
	if err != nil {
		return "", fmt.Errorf("codex reset credit consume: %w", err)
	}
	var r struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("codex reset credit consume: parsing response: %w", err)
	}
	if r.Code == "" {
		return "", fmt.Errorf("codex reset credit consume: no outcome in response: %s", truncate(body, 200))
	}
	return normalizeRedeemOutcome(r.Code), nil
}

// normalizeRedeemOutcome folds the two spellings of the same outcomes into the
// snake_case form we report: the private endpoint answers in snake_case, while
// Codex's app-server protocol spells them in camelCase. Unknown codes pass
// through untouched rather than being reported as a success.
func normalizeRedeemOutcome(code string) string {
	switch code {
	case "nothingToReset":
		return RedeemNothingToReset
	case "noCredit":
		return RedeemNoCredit
	case "alreadyRedeemed":
		return RedeemAlreadyRedeemed
	default:
		return code
	}
}

func randomIdempotencyKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("limitping-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// creditIdempotencyKey derives a stable key from the credit being spent, so the
// same credit always maps to the same logical attempt.
func creditIdempotencyKey(c usage.ResetCredit) string {
	sum := sha256.Sum256([]byte("limitping-reset-credit|" + c.ExpiresAt.UTC().Format(time.RFC3339)))
	return hex.EncodeToString(sum[:16])
}

func codexActiveTask(_ context.Context) (string, bool, error) {
	// Active-session detection relies entirely on the Codex CLI hooks (see
	// `limitping hooks install`). Without them we don't guess from the process
	// list; the scheduler just pings.
	if !activity.Enabled("codex") {
		return "", false, nil
	}
	return activity.Active("codex")
}

type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int     `json:"limit_window_seconds"`
	ResetAfterSeconds  int     `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

// The windows are pointers because the backend nulls out a window when that
// limit is not currently enforced. OpenAI did exactly that between 2026-07-12
// and (at the latest) 2026-09-09, when the 5h limit was gone and primary_window
// carried the weekly one — hence codexWindowsFromRateLimit classifying by
// length rather than by position. Note that a limit which is enforced but has no
// window running is a different thing entirely, and is not nulled out: see
// codexWindowAnchored.
type codexRateLimit struct {
	Allowed      bool         `json:"allowed"`
	LimitReached bool         `json:"limit_reached"`
	Primary      *codexWindow `json:"primary_window"`
	Secondary    *codexWindow `json:"secondary_window"`
}

type codexCredits struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

type codexUsageResp struct {
	PlanType     string                   `json:"plan_type"`
	RateLimit    codexRateLimit           `json:"rate_limit"`
	Credits      *codexCredits            `json:"credits"`
	ResetCredits *codexInlineResetCredits `json:"rate_limit_reset_credits"`
}

// codexInlineResetCredits is the reset-credit count embedded in the usage
// response itself; a fallback when the detail endpoint is unavailable.
type codexInlineResetCredits struct {
	AvailableCount int `json:"available_count"`
}

type codexResetCreditsResp struct {
	AvailableCount *int               `json:"available_count"`
	Credits        []codexResetCredit `json:"credits"`
}

type codexResetCredit struct {
	Status     string `json:"status"`
	GrantedAt  string `json:"granted_at"`
	ExpiresAt  string `json:"expires_at"`
	RedeemedAt string `json:"redeemed_at"`
}

func readCodexUsage(ctx context.Context, auth *auth.CodexAuth) ([]byte, codexUsageResp, error) {
	var r codexUsageResp
	accountID, _ := auth.AccountID(ctx)
	body, err := fetchWithAuth(ctx, auth, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", codexUserAgent)
		if accountID != "" {
			req.Header.Set("ChatGPT-Account-Id", accountID)
		}
		return req, nil
	})
	if err != nil {
		return nil, r, err
	}

	if err := json.Unmarshal(body, &r); err != nil {
		return nil, r, fmt.Errorf("codex usage: parsing response: %w", err)
	}
	return body, r, nil
}

func readCodexResetCredits(ctx context.Context, auth *auth.CodexAuth) (*usage.ResetCredits, error) {
	var r codexResetCreditsResp
	accountID, _ := auth.AccountID(ctx)
	body, err := fetchWithAuth(ctx, auth, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexResetCreditsURL(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", codexUserAgent)
		req.Header.Set("OpenAI-Beta", "codex-1")
		req.Header.Set("originator", "Codex Desktop")
		if accountID != "" {
			req.Header.Set("ChatGPT-Account-Id", accountID)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("codex reset credits: parsing response: %w", err)
	}
	return codexResetCreditsToUsage(r), nil
}

func codexUsageToUsage(provider string, body []byte, r codexUsageResp) *usage.Usage {
	now := time.Now()
	fiveHour, weekly := codexWindowsFromRateLimit(r.RateLimit, now)
	u := &usage.Usage{
		Provider:     provider,
		Plan:         r.PlanType,
		FetchedAt:    now,
		Raw:          body,
		LimitReached: r.RateLimit.LimitReached,
		FiveHour:     fiveHour,
		Weekly:       weekly,
	}
	if r.Credits != nil {
		u.Credits = &usage.Credits{
			HasCredits: r.Credits.HasCredits,
			Unlimited:  r.Credits.Unlimited,
			Balance:    r.Credits.Balance,
		}
	}
	return u
}

func codexResetCreditsToUsage(r codexResetCreditsResp) *usage.ResetCredits {
	credits := make([]usage.ResetCredit, 0, len(r.Credits))
	for _, c := range r.Credits {
		credits = append(credits, usage.ResetCredit{
			Status:     c.Status,
			GrantedAt:  parseCodexResetTime(c.GrantedAt),
			ExpiresAt:  parseCodexResetTime(c.ExpiresAt),
			RedeemedAt: parseCodexResetTime(c.RedeemedAt),
		})
	}
	count := len(credits)
	if r.AvailableCount != nil {
		count = *r.AvailableCount
	}
	return &usage.ResetCredits{
		AvailableCount: count,
		Credits:        credits,
	}
}

func parseCodexResetTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

func codexUsageURL() string {
	base := codexDefaultBaseURL
	if contents, err := os.ReadFile(codexConfigPath()); err == nil {
		if configured := parseCodexBaseURL(string(contents)); configured != "" {
			base = configured
		}
	}
	return codexUsageURLFromBase(base)
}

func codexResetCreditsURL() string {
	base := codexDefaultBaseURL
	if contents, err := os.ReadFile(codexConfigPath()); err == nil {
		if configured := parseCodexBaseURL(string(contents)); configured != "" {
			base = configured
		}
	}
	return codexResetCreditsURLFromBase(base)
}

func codexUsageURLFromBase(base string) string {
	normalized := normalizeCodexBaseURL(base)
	path := codexAPIPath
	if strings.Contains(normalized, "/backend-api") {
		path = codexChatGPTPath
	}
	endpoint := normalized + path
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return codexDefaultBaseURL + codexChatGPTPath
	}
	return endpoint
}

func codexResetCreditsURLFromBase(base string) string {
	return codexResetURLFromBase(base, codexResetPath)
}

func codexConsumeURL() string {
	base := codexDefaultBaseURL
	if contents, err := os.ReadFile(codexConfigPath()); err == nil {
		if configured := parseCodexBaseURL(string(contents)); configured != "" {
			base = configured
		}
	}
	return codexResetURLFromBase(base, codexConsumePath)
}

// codexResetURLFromBase builds a reset-credit endpoint. These live only on the
// ChatGPT backend, so a base pointing elsewhere falls back to the default.
func codexResetURLFromBase(base, path string) string {
	normalized := normalizeCodexBaseURL(base)
	if !strings.Contains(normalized, "/backend-api") {
		normalized = codexDefaultBaseURL
	}
	endpoint := normalized + path
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return codexDefaultBaseURL + path
	}
	return endpoint
}

func normalizeCodexBaseURL(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = codexDefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	if (strings.HasPrefix(base, "https://chatgpt.com") || strings.HasPrefix(base, "https://chat.openai.com")) &&
		!strings.Contains(base, "/backend-api") {
		base += "/backend-api"
	}
	return base
}

func parseCodexBaseURL(contents string) string {
	var cfg struct {
		ChatGPTBaseURL string `toml:"chatgpt_base_url"`
	}
	if _, err := toml.Decode(contents, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.ChatGPTBaseURL)
}

// codexPingModel resolves the model to pass to `codex -m`. An unset config
// means "spend as little as possible", not "use my Codex working model": the
// cheapest catalogued model is picked explicitly. Empty means the catalog could
// not answer, and the CLI is left to choose as before.
func codexPingModel(configured string) string {
	if configured != "" {
		return configured
	}
	return codexCheapestModel()
}

// codexCLIConfiguredModel is the model set in the Codex CLI's own config, used
// only to report what an un-pinned ping will run on. It is never chosen: a
// working model is typically a much more expensive tier than a ping needs.
func codexCLIConfiguredModel() string {
	contents, err := os.ReadFile(codexConfigPath())
	if err != nil {
		return ""
	}
	return parseCodexModel(string(contents))
}

func parseCodexModel(contents string) string {
	var cfg struct {
		Model string `toml:"model"`
	}
	if _, err := toml.Decode(contents, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Model)
}

func codexConfigPath() string {
	dir, err := auth.CodexHome()
	if err != nil {
		return filepath.Join(".codex", "config.toml")
	}
	return filepath.Join(dir, "config.toml")
}

func codexModelsCachePath() string {
	return filepath.Join(filepath.Dir(codexConfigPath()), "models_cache.json")
}

// checkCodexModel rejects a configured model the Codex CLI's own catalog no
// longer lists. OpenAI retires Codex models every few months and `codex -m`
// is not validated locally, so without this a stale config fails as an opaque
// server-side error at window rollover — precisely when nobody is watching.
// The catalog is a private Codex file: any problem reading it skips the check
// rather than blocking a ping that would otherwise have worked.
func checkCodexModel(model string) error {
	if model == "" {
		return nil // resolved from the catalog, so never stale
	}
	catalog := codexModelCatalog()
	if len(catalog) == 0 {
		return nil
	}
	var available []string
	for _, m := range catalog {
		if m.Slug == model {
			return nil
		}
		if m.Listed {
			available = append(available, m.Slug)
		}
	}
	if len(available) == 0 {
		for _, m := range catalog {
			available = append(available, m.Slug)
		}
	}
	return fmt.Errorf("codex model %q is no longer in the Codex model catalog (%s); available: %s — update model under [codex] in limitping's config, or set it to \"\" to let limitping pick the cheapest one",
		model, codexModelsCachePath(), strings.Join(available, ", "))
}

// codexCatalogModel is one entry of the Codex CLI's cached model catalog.
// Listed reflects visibility: the catalog hides internal models such as
// codex-auto-review, which are valid to pass but must never be chosen or
// suggested on the user's behalf.
type codexCatalogModel struct {
	Slug        string
	Description string
	Priority    int
	Listed      bool
}

// codexModelCatalog reads the models the Codex CLI last cached for this
// account. It returns nil when the cache is missing or unparseable.
func codexModelCatalog() []codexCatalogModel {
	data, err := os.ReadFile(codexModelsCachePath())
	if err != nil {
		return nil
	}
	var cache struct {
		Models []struct {
			Slug        string `json:"slug"`
			Description string `json:"description"`
			Visibility  string `json:"visibility"`
			Priority    int    `json:"priority"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	models := make([]codexCatalogModel, 0, len(cache.Models))
	for _, m := range cache.Models {
		if m.Slug == "" {
			continue
		}
		models = append(models, codexCatalogModel{
			Slug:        m.Slug,
			Description: m.Description,
			Priority:    m.Priority,
			Listed:      m.Visibility == "list",
		})
	}
	return models
}

// codexBudgetMarkers are the words OpenAI uses for its low-cost tier, matched
// against a model's description and slug. The catalog carries no price field,
// so this wording is the only cost signal it exposes ("Fast and affordable
// agentic coding model"), alongside the "mini"-style naming used for budget
// variants.
var codexBudgetMarkers = []string{"affordable", "cheap", "low cost", "low-cost", "mini"}

// codexCheapestModel picks the model a ping should use when none is configured.
// A ping only has to be a billable request — the model does not matter — so it
// should land on the cheapest one the plan offers rather than on whatever the
// user set as their working model in the Codex CLI, which is typically a far
// more expensive tier.
//
// Ties break toward the largest priority, i.e. the entry Codex itself ranks
// furthest from its flagship. Returns "" when the catalog is unreadable or
// nothing is recognizably the budget tier: guessing a model on price wording
// that no longer exists would be worse than letting the CLI decide.
func codexCheapestModel() string {
	best := codexCatalogModel{Priority: -1}
	for _, m := range codexModelCatalog() {
		if !m.Listed || !codexIsBudgetModel(m) {
			continue
		}
		// Largest priority wins; slug breaks an exact tie so the choice is
		// stable across catalog orderings.
		if m.Priority > best.Priority || (m.Priority == best.Priority && m.Slug < best.Slug) {
			best = m
		}
	}
	return best.Slug
}

func codexIsBudgetModel(m codexCatalogModel) bool {
	haystack := strings.ToLower(m.Description + " " + m.Slug)
	for _, marker := range codexBudgetMarkers {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

// codexWindowsFromRateLimit classifies the windows by length rather than
// position. Historically primary was the 5h window and secondary the weekly
// one, but with the 5h limit removed the weekly window is the (only) primary,
// so position no longer identifies a window. A window a couple of days or
// longer is the weekly one; anything shorter is the 5h one. A limit whose
// window is absent stays the zero Window (usage.Window.Missing).
func codexWindowsFromRateLimit(rl codexRateLimit, now time.Time) (fiveHour, weekly usage.Window) {
	const weeklyMinSeconds = 2 * 24 * 60 * 60
	for _, w := range []*codexWindow{rl.Primary, rl.Secondary} {
		if w == nil {
			continue
		}
		if w.LimitWindowSeconds >= weeklyMinSeconds {
			weekly = codexWindowToUsage(*w, now)
		} else {
			fiveHour = codexWindowToUsage(*w, now)
		}
	}
	return fiveHour, weekly
}

// codexWindowToUsage normalizes one window. A window no request has started
// carries no reset time, which is how usage.Window says "no active window" —
// see codexWindowAnchored for why the backend's own reset time cannot be taken
// at face value.
func codexWindowToUsage(w codexWindow, now time.Time) usage.Window {
	out := usage.Window{
		UsedPercent:   w.UsedPercent,
		WindowSeconds: w.LimitWindowSeconds,
	}
	if w.ResetAt > 0 && codexWindowAnchored(w, now) {
		out.ResetsAt = time.Unix(w.ResetAt, 0)
	}
	return out
}

// codexWindowAnchored reports whether a request has actually started this
// window. The backend never says "no window is running": it answers with a
// full-length one that slides forward on every read — used_percent 0 and
// reset_after_seconds equal to limit_window_seconds, i.e. "here is when a window
// would end if you started one now". Verified 2026-09-19 on an idle 5h window:
// two reads 11s apart both returned reset_at = now + 18000.
//
// So the length that is left is the signal. A window a request anchored has
// strictly less of itself remaining than its own length, and its reset time
// holds still across reads; an unanchored one has all of it left, every time.
// Taking that at face value is what made watch sit on a window it had never
// started and never ping.
//
// reset_after_seconds is the server's own countdown, so the comparison is immune
// to clock skew here. Only when that field is absent does it fall back to the
// local clock, and then with a tolerance, so skew alone cannot make an idle
// window look anchored.
func codexWindowAnchored(w codexWindow, now time.Time) bool {
	if w.LimitWindowSeconds <= 0 {
		return false
	}
	if w.ResetAfterSeconds > 0 {
		return w.ResetAfterSeconds < w.LimitWindowSeconds
	}
	remaining := time.Unix(w.ResetAt, 0).Sub(now)
	return remaining < time.Duration(w.LimitWindowSeconds)*time.Second-codexAnchorSkew
}

func triggerCodex(ctx context.Context, cfg config.ProviderConfig, dryRun bool) (*TriggerResult, error) {
	prompt := cfg.Prompt
	if prompt == "" {
		prompt = "ok"
	}
	// --ephemeral is why the ping runs headless rather than through the TUI:
	// the interactive CLI has no way to skip persisting a session, so every
	// ping left an "ok" conversation behind in `codex resume` and in the Codex
	// Desktop thread list. --json is what makes the ping checkable at all — the
	// turn.completed event is the only local proof that a billable request went
	// out, which is what starts the window.
	//
	// Hooks stay off because a ping is a synthetic session: the user's hooks
	// have no business firing for it, and it must not register itself as an
	// active Codex session. The sandbox is pinned read-only because nothing
	// reviews what the model does here — unlike an interactive session, which
	// has a human at the keys.
	args := []string{
		"exec", "--ephemeral", "--json",
		"--skip-git-repo-check",
		"--disable", "hooks",
		"--sandbox", "read-only",
	}
	if cfg.ReasoningEffort != "" {
		args = append(args, "-c", "model_reasoning_effort="+cfg.ReasoningEffort)
	}
	model := codexPingModel(cfg.Model)
	if model != "" {
		args = append(args, "-m", model)
	}
	args = append(args, codexExecArgs(cfg.ExtraArgs)...)
	args = append(args, prompt)
	reported := model
	if reported == "" {
		// Nothing was pinned, so the CLI picks; report its choice rather than
		// leaving the ping silent about what it spent quota on.
		reported = codexCLIConfiguredModel()
	}
	res := &TriggerResult{
		Command: "codex " + shellJoin(args),
		Model:   reported,
	}
	// Checked before the dry-run return too: a dry run that prints a command
	// which cannot succeed is worse than no dry run.
	if err := checkCodexModel(cfg.Model); err != nil {
		return res, err
	}
	if dryRun {
		return res, nil
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Left nil so the child reads /dev/null: given a prompt argument and an
	// open stdin, `codex exec` waits to append piped input to it.
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return res, fmt.Errorf("codex exec failed: %w: %s", err, codexExecTail(stderr, stdout))
	}

	cached, completed := codexExecUsage(stdout.Bytes(), res)
	if !completed {
		// A clean exit with no completed turn means nothing reached the model,
		// so no window was started. This is the one outcome a ping must never
		// report as success: watch would record it and then wait out a window
		// that never began.
		return res, fmt.Errorf("codex exec started no turn, so no window was started: %s",
			codexExecTail(stderr, stdout))
	}
	// Codex doesn't report a USD cost; derive it from LiteLLM rates like
	// CodexBar/ccusage do.
	if reported != "" {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		if price, ok := pricing.Default().Lookup(pctx, reported); ok {
			res.CostUSD = price.Cost(res.InputTokens, cached, res.OutputTokens)
		}
		pcancel()
	}
	return res, nil
}

// codexExecUsage reads the turn's token usage out of `codex exec --json`
// output, which is JSONL whose final turn.completed event carries the totals.
// output_tokens already includes reasoning tokens, so they are not added again.
// It reports whether a completed turn was seen at all — the ping's only local
// evidence that a billable request was dispatched.
func codexExecUsage(out []byte, res *TriggerResult) (cachedInput int, completed bool) {
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev struct {
			Type  string `json:"type"`
			Usage *struct {
				InputTokens       int `json:"input_tokens"`
				CachedInputTokens int `json:"cached_input_tokens"`
				OutputTokens      int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(line, &ev); err != nil || ev.Type != "turn.completed" || ev.Usage == nil {
			continue
		}
		res.InputTokens = ev.Usage.InputTokens
		res.OutputTokens = ev.Usage.OutputTokens
		res.TotalTokens = res.InputTokens + res.OutputTokens
		res.HasUsage = true
		cachedInput, completed = ev.Usage.CachedInputTokens, true
	}
	return cachedInput, completed
}

// codexExecTail renders whatever the CLI said, preferring stderr: with --json,
// stdout is an event stream and the human-readable failure lands on stderr.
func codexExecTail(stderr, stdout bytes.Buffer) string {
	if tail := truncate(stderr.Bytes(), 300); tail != "" {
		return tail
	}
	return truncate(stdout.Bytes(), 300)
}

// codexExecArgs drops the flags that exist only on the interactive CLI, so an
// extra_args list carried over from when the ping ran through the TUI cannot
// make `codex exec` reject the entire command line.
func codexExecArgs(extra []string) []string {
	out := make([]string, 0, len(extra))
	for i := 0; i < len(extra); i++ {
		arg := extra[i]
		flag, inlineValue := splitFlagValue(arg)
		if codexExecUnsupportedValueArg(flag) {
			if !inlineValue && i+1 < len(extra) {
				i++
			}
			continue
		}
		if codexExecUnsupportedArg(flag) {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func codexExecUnsupportedArg(flag string) bool {
	switch flag {
	case "--no-alt-screen":
		return true
	default:
		return false
	}
}

func codexExecUnsupportedValueArg(flag string) bool {
	switch flag {
	case "--remote", "--remote-auth-token-env":
		return true
	default:
		return false
	}
}
