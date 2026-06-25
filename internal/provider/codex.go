package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/creack/pty"

	"github.com/wavever/CCLimitPing/internal/activity"
	"github.com/wavever/CCLimitPing/internal/auth"
	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/usage"
)

const (
	codexDefaultBaseURL = "https://chatgpt.com/backend-api"
	codexChatGPTPath    = "/wham/usage"
	codexAPIPath        = "/api/codex/usage"
	codexUserAgent      = "limitping"

	codexTurnMinWait  = 4 * time.Second
	codexTurnQuiet    = 2500 * time.Millisecond
	codexTurnMaxWait  = 45 * time.Second
	codexExitGrace    = 5 * time.Second
	codexPollInterval = 200 * time.Millisecond
)

// Codex reads usage via the ChatGPT backend usage endpoint and triggers windows
// via the interactive, TTY-backed Codex CLI. Headless `codex exec` can consume
// tokens without anchoring the subscription-backed Codex window.
type Codex struct {
	name             string
	activityProvider string
	cfg              config.ProviderConfig
	auth             *auth.CodexAuth
}

func NewCodex(cfg config.ProviderConfig) *Codex {
	return newCodexBacked("codex", "codex", cfg)
}

// NewSpark returns a Codex-backed provider that uses the Spark Codex model but
// reports itself as an independent provider to the scheduler and CLI.
func NewSpark(cfg config.ProviderConfig) *Codex {
	return newCodexBacked("spark", "codex", cfg)
}

func newCodexBacked(name, activityProvider string, cfg config.ProviderConfig) *Codex {
	return &Codex{
		name:             name,
		activityProvider: activityProvider,
		cfg:              cfg,
		auth:             auth.NewCodexAuth(),
	}
}

func (c *Codex) Name() string { return c.name }

func (c *Codex) ActiveTask(_ context.Context) (string, bool, error) {
	// Active-session detection relies entirely on the CLI hooks (see `limitping
	// hooks install`). Without them we don't guess from the process list — the
	// scheduler just pings.
	if !activity.Enabled(c.activityProvider) {
		return "", false, nil
	}
	return activity.Active(c.activityProvider)
}

type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int     `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type codexUsageResp struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		Allowed      bool        `json:"allowed"`
		LimitReached bool        `json:"limit_reached"`
		Primary      codexWindow `json:"primary_window"`
		Secondary    codexWindow `json:"secondary_window"`
	} `json:"rate_limit"`
	Credits *struct {
		HasCredits bool   `json:"has_credits"`
		Unlimited  bool   `json:"unlimited"`
		Balance    string `json:"balance"`
	} `json:"credits"`
}

func (c *Codex) ReadUsage(ctx context.Context) (*usage.Usage, error) {
	accountID, _ := c.auth.AccountID(ctx)
	body, err := fetchWithAuth(ctx, c.auth, func(token string) (*http.Request, error) {
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
		return nil, err
	}

	var r codexUsageResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("codex usage: parsing response: %w", err)
	}

	u := &usage.Usage{
		Provider:     c.Name(),
		Plan:         r.PlanType,
		FetchedAt:    time.Now(),
		Raw:          body,
		LimitReached: r.RateLimit.LimitReached,
		FiveHour:     codexWindowToUsage(r.RateLimit.Primary),
		Weekly:       codexWindowToUsage(r.RateLimit.Secondary),
	}
	if r.Credits != nil {
		u.Credits = &usage.Credits{
			HasCredits: r.Credits.HasCredits,
			Unlimited:  r.Credits.Unlimited,
			Balance:    r.Credits.Balance,
		}
	}
	return u, nil
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

func codexConfigPath() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return filepath.Join(h, "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".codex", "config.toml")
	}
	return filepath.Join(home, ".codex", "config.toml")
}

func codexWindowToUsage(w codexWindow) usage.Window {
	var resetsAt time.Time
	if w.ResetAt > 0 {
		resetsAt = time.Unix(w.ResetAt, 0)
	}
	return usage.Window{
		UsedPercent:   w.UsedPercent,
		ResetsAt:      resetsAt,
		WindowSeconds: w.LimitWindowSeconds,
	}
}

func (c *Codex) Trigger(ctx context.Context, dryRun bool) (*TriggerResult, error) {
	prompt := c.cfg.Prompt
	if prompt == "" {
		prompt = "ok"
	}
	args := []string{}
	if c.cfg.ReasoningEffort != "" {
		args = append(args, "-c", "model_reasoning_effort="+c.cfg.ReasoningEffort)
	}
	if c.cfg.Model != "" {
		args = append(args, "-m", c.cfg.Model)
	}
	args = append(args, codexInteractiveArgs(c.cfg.ExtraArgs)...)
	args = append(args, prompt)
	res := &TriggerResult{Command: "codex " + shellJoin(args)}
	if dryRun {
		return res, nil
	}

	cmd := exec.CommandContext(ctx, "codex", args...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return res, fmt.Errorf("codex interactive failed to start: %w", err)
	}
	defer ptmx.Close()

	output := &limitedBuffer{limit: 4096}
	go func() {
		_, _ = io.Copy(output, ptmx)
	}()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	if terminal, err := codexAwait(ctx, cmd, ptmx, output, done, codexTurnMaxWait,
		func(idle, elapsed time.Duration) bool {
			return elapsed >= codexTurnMinWait && idle >= codexTurnQuiet
		}); terminal {
		return res, err
	}

	return res, codexInteractiveStop(ctx, cmd, ptmx, done, output)
}

func codexAwait(ctx context.Context, cmd *exec.Cmd, ptmx *os.File, output *limitedBuffer, done <-chan error, maxWait time.Duration, ready func(idle, elapsed time.Duration) bool) (bool, error) {
	start := time.Now()
	deadline := time.After(maxWait)
	ticker := time.NewTicker(codexPollInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return true, codexInteractiveErr(err, output)
		case <-ctx.Done():
			return true, codexInteractiveCancel(ctx, cmd, ptmx, done, output)
		case <-deadline:
			return false, nil
		case <-ticker.C:
			changed := output.changedAt()
			if !changed.IsZero() && ready(time.Since(changed), time.Since(start)) {
				return false, nil
			}
		}
	}
}

func codexInteractiveStop(ctx context.Context, cmd *exec.Cmd, ptmx *os.File, done <-chan error, output *limitedBuffer) error {
	deadline := time.After(codexExitGrace)
	ticker := time.NewTicker(codexExitGrace / 2)
	defer ticker.Stop()

	for sent := false; ; {
		if !sent {
			_, _ = ptmx.Write([]byte{0x03})
			sent = true
		}
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return codexInteractiveCancel(ctx, cmd, ptmx, done, output)
		case <-ticker.C:
			_, _ = ptmx.Write([]byte{0x03})
		case <-deadline:
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return nil
		}
	}
}

func codexInteractiveErr(err error, output *limitedBuffer) error {
	if err == nil {
		return nil
	}
	tail := truncate(output.Bytes(), 300)
	if tail == "" {
		return fmt.Errorf("codex interactive failed: %w", err)
	}
	return fmt.Errorf("codex interactive failed: %w: %s", err, tail)
}

func codexInteractiveCancel(ctx context.Context, cmd *exec.Cmd, ptmx *os.File, done <-chan error, output *limitedBuffer) error {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = ptmx.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	tail := truncate(output.Bytes(), 300)
	if tail == "" {
		return fmt.Errorf("codex interactive cancelled: %w", ctx.Err())
	}
	return fmt.Errorf("codex interactive cancelled: %w: %s", ctx.Err(), tail)
}

func codexInteractiveArgs(extra []string) []string {
	out := make([]string, 0, len(extra))
	for i := 0; i < len(extra); i++ {
		arg := extra[i]
		flag, inlineValue := splitFlagValue(arg)
		if codexInteractiveUnsupportedValueArg(flag) {
			if !inlineValue && i+1 < len(extra) {
				i++
			}
			continue
		}
		if codexInteractiveUnsupportedArg(flag) {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func codexInteractiveUnsupportedArg(flag string) bool {
	switch flag {
	case "--skip-git-repo-check", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--json":
		return true
	default:
		return false
	}
}

func codexInteractiveUnsupportedValueArg(flag string) bool {
	switch flag {
	case "--output-schema", "--output-last-message", "--color", "-o":
		return true
	default:
		return false
	}
}
