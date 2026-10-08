package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// codexOAuthClientID is the Codex CLI public OAuth client id, used for the
// refresh-token grant against the OpenAI auth server.
const codexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

const codexTokenEndpoint = "https://auth.openai.com/oauth/token"

// CodexAuth loads and refreshes the Codex OAuth tokens stored in
// ~/.codex/auth.json (or $CODEX_HOME/auth.json).
type CodexAuth struct {
	mu        sync.Mutex
	access    string
	refresh   string
	accountID string
	raw       map[string]any // full auth.json, preserved on write-back
}

func NewCodexAuth() *CodexAuth { return &CodexAuth{} }

// Token returns a cached access token, loading from auth.json on first use.
func (a *CodexAuth) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.access != "" {
		return a.access, nil
	}
	if err := a.loadLocked(); err != nil {
		return "", err
	}
	return a.access, nil
}

// AccountID returns the ChatGPT account id, sent as the chatgpt-account-id
// header on usage requests.
func (a *CodexAuth) AccountID(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.access == "" {
		if err := a.loadLocked(); err != nil {
			return "", err
		}
	}
	return a.accountID, nil
}

// Reload forces a re-read from auth.json (the Codex CLI may have refreshed it).
func (a *CodexAuth) Reload(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.loadLocked(); err != nil {
		return "", err
	}
	return a.access, nil
}

// Refresh exchanges the refresh token for a new access token and writes the
// rotated tokens back to auth.json so the Codex CLI stays in sync.
func (a *CodexAuth) Refresh(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.refresh == "" {
		if err := a.loadLocked(); err != nil {
			return "", err
		}
	}
	if a.refresh == "" {
		return "", fmt.Errorf("codex: no refresh token available")
	}

	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": a.refresh,
		"client_id":     codexOAuthClientID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("codex token refresh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("codex token refresh: HTTP %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("codex token refresh: empty access_token")
	}
	a.access = tok.AccessToken
	if tok.RefreshToken != "" {
		a.refresh = tok.RefreshToken
	}
	a.persistLocked(tok.IDToken)
	return a.access, nil
}

// CodexHome is the directory the Codex CLI keeps its state in: $CODEX_HOME when
// set, else ~/.codex. auth.json, config.toml, hooks.json and the session
// rollouts all live under it.
func CodexHome() (string, error) {
	if h := strings.TrimSpace(os.Getenv("CODEX_HOME")); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func codexAuthPath() (string, error) {
	dir, err := CodexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "auth.json"), nil
}

func (a *CodexAuth) loadLocked() error {
	path, err := codexAuthPath()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("codex credentials not found at %s: %w", path, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("codex credentials: invalid JSON: %w", err)
	}
	a.raw = raw
	tokens, _ := raw["tokens"].(map[string]any)
	if tokens == nil {
		return fmt.Errorf("codex credentials: no tokens object in %s", path)
	}
	a.access, _ = tokens["access_token"].(string)
	a.refresh, _ = tokens["refresh_token"].(string)
	a.accountID, _ = tokens["account_id"].(string)
	if a.access == "" {
		return fmt.Errorf("codex credentials: no access_token in %s", path)
	}
	return nil
}

func (a *CodexAuth) persistLocked(idToken string) {
	if a.raw == nil {
		a.raw = map[string]any{}
	}
	tokens, _ := a.raw["tokens"].(map[string]any)
	if tokens == nil {
		tokens = map[string]any{}
		a.raw["tokens"] = tokens
	}
	tokens["access_token"] = a.access
	tokens["refresh_token"] = a.refresh
	if idToken != "" {
		tokens["id_token"] = idToken
	}
	a.raw["last_refresh"] = time.Now().UTC().Format(time.RFC3339)

	path, err := codexAuthPath()
	if err != nil {
		return
	}
	blob, err := json.MarshalIndent(a.raw, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, blob, 0o600)
}
