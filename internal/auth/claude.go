// Package auth loads (and, when necessary, refreshes) the OAuth credentials
// that Claude Code and Codex already store on disk / in the Keychain. We reuse
// the official tools' credentials rather than managing our own login.
package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// claudeOAuthClientID is Claude Code's public OAuth client id, used for the
// refresh-token grant. Refresh rotates the token in the same store the official
// CLI reads, keeping both in sync. If Anthropic changes this, update here.
const claudeOAuthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

const (
	claudeKeychainServiceBase = "Claude Code-credentials"
	// Claude Code refreshes against platform.claude.com since 2.1.2xx; the old
	// console.anthropic.com address still answers, but only for now.
	claudeTokenEndpoint = "https://platform.claude.com/v1/oauth/token"
)

// ClaudeConfigDir is the directory Claude Code keeps its user config in:
// $CLAUDE_CONFIG_DIR when set, else ~/.claude. settings.json, the plaintext
// credentials file and the transcripts all live under it.
func ClaudeConfigDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// ClaudeGlobalConfigPath is Claude Code's global config file, .claude.json,
// which holds the login's account and organization. Unlike settings.json it
// sits in the home directory itself, not in ~/.claude — unless
// $CLAUDE_CONFIG_DIR is set, which moves it there.
func ClaudeGlobalConfigPath() (string, error) {
	dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = home
	}
	return filepath.Join(dir, ".claude.json"), nil
}

// claudeSecureStorageDir is where Claude Code keys its credentials, which is
// the config dir unless CLAUDE_SECURESTORAGE_CONFIG_DIR overrides it (an empty
// override means the default ~/.claude). custom reports whether the location
// is anything but the default, since only then does the Keychain entry get a
// per-directory name.
func claudeSecureStorageDir() (dir string, custom bool, err error) {
	if v, ok := os.LookupEnv("CLAUDE_SECURESTORAGE_CONFIG_DIR"); ok {
		if v == "" {
			home, herr := os.UserHomeDir()
			if herr != nil {
				return "", false, herr
			}
			return filepath.Join(home, ".claude"), false, nil
		}
		return v, true, nil
	}
	custom = strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")) != ""
	dir, err = ClaudeConfigDir()
	return dir, custom, err
}

// claudeKeychainService names the Keychain item the way Claude Code does: a
// non-default config dir gets its own item, suffixed with a hash of the dir,
// so several Claude Code installs never share one login.
func claudeKeychainService() string {
	dir, custom, err := claudeSecureStorageDir()
	if err != nil || !custom {
		return claudeKeychainServiceBase
	}
	sum := sha256.Sum256([]byte(dir))
	return claudeKeychainServiceBase + "-" + hex.EncodeToString(sum[:])[:8]
}

// claudeCredentialsPath is the plaintext store Claude Code falls back to off
// macOS (and on macOS when the Keychain is unavailable).
func claudeCredentialsPath() (string, error) {
	dir, _, err := claudeSecureStorageDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".credentials.json"), nil
}

// authHTTPClient performs the OAuth refresh requests; swapped in tests (the
// same seam the provider package uses for its usage client).
var authHTTPClient = http.DefaultClient

// claudeKeychainEnabled gates the macOS Keychain path. Tests disable it so
// they never read from — or write fake credentials into — the real Keychain.
var claudeKeychainEnabled = runtime.GOOS == "darwin"

// ClaudeAuth provides a current Claude access token, reloading from the store
// (Keychain on macOS, ~/.claude/.credentials.json elsewhere) and refreshing via
// the refresh token when needed.
type ClaudeAuth struct {
	mu      sync.Mutex
	access  string
	refresh string
	account string         // Keychain account, needed for write-back (macOS)
	wrapper map[string]any // full "claudeAiOauth" object, preserved on write-back
}

// NewClaudeAuth returns an empty holder; the token is loaded lazily.
func NewClaudeAuth() *ClaudeAuth { return &ClaudeAuth{} }

// Token returns a cached access token, loading from the store on first use.
func (a *ClaudeAuth) Token(ctx context.Context) (string, error) {
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

// Reload forces a re-read from the store (e.g. the official CLI may have
// refreshed the token since we last read it) and returns the fresh token.
func (a *ClaudeAuth) Reload(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.loadLocked(); err != nil {
		return "", err
	}
	return a.access, nil
}

// Refresh exchanges the refresh token for a new access token and writes the
// rotated credentials back to the store so the official CLI stays in sync.
func (a *ClaudeAuth) Refresh(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.refresh == "" {
		if err := a.loadLocked(); err != nil {
			return "", err
		}
	}
	if a.refresh == "" {
		return "", fmt.Errorf("claude: no refresh token available")
	}

	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": a.refresh,
		"client_id":     claudeOAuthClientID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeTokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("claude token refresh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("claude token refresh: HTTP %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("claude token refresh: empty access_token")
	}
	a.access = tok.AccessToken
	if tok.RefreshToken != "" {
		a.refresh = tok.RefreshToken
	}
	a.persistLocked(tok.ExpiresIn)
	return a.access, nil
}

func (a *ClaudeAuth) loadLocked() error {
	raw, account, err := readClaudeBlob()
	if err != nil {
		return err
	}
	a.account = account
	var outer map[string]any
	if err := json.Unmarshal(raw, &outer); err != nil {
		return fmt.Errorf("claude credentials: invalid JSON: %w", err)
	}
	// Credentials are stored either as {"claudeAiOauth": {...}} or flat.
	wrapper := outer
	if inner, ok := outer["claudeAiOauth"].(map[string]any); ok {
		wrapper = inner
	}
	a.wrapper = wrapper
	a.access, _ = wrapper["accessToken"].(string)
	a.refresh, _ = wrapper["refreshToken"].(string)
	if a.access == "" {
		return fmt.Errorf("claude credentials: no accessToken found")
	}
	return nil
}

// persistLocked writes the updated tokens back to the same store, preserving
// other fields. Best-effort: a failure here doesn't break the running session,
// it just means we may have to refresh again next time.
func (a *ClaudeAuth) persistLocked(expiresIn int64) {
	if a.wrapper == nil {
		a.wrapper = map[string]any{}
	}
	a.wrapper["accessToken"] = a.access
	a.wrapper["refreshToken"] = a.refresh
	if expiresIn > 0 {
		a.wrapper["expiresAt"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()
	}
	out := map[string]any{"claudeAiOauth": a.wrapper}
	blob, err := json.Marshal(out)
	if err != nil {
		return
	}
	_ = writeClaudeBlob(blob, a.account)
}

// readClaudeBlob returns the raw credentials JSON and (on macOS) the Keychain
// account name for write-back.
func readClaudeBlob() (raw []byte, account string, err error) {
	service := claudeKeychainService()
	if claudeKeychainEnabled {
		out, err := exec.Command("security", "find-generic-password",
			"-s", service, "-w").Output()
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			return bytes.TrimSpace(out), keychainAccount(service), nil
		}
		// fall through to file fallback
	}
	path, perr := claudeCredentialsPath()
	if perr != nil {
		return nil, "", perr
	}
	b, ferr := os.ReadFile(path)
	if ferr != nil {
		if claudeKeychainEnabled {
			return nil, "", fmt.Errorf("claude credentials not found in Keychain (%q) or %s", service, path)
		}
		return nil, "", fmt.Errorf("claude credentials not found at %s: %w", path, ferr)
	}
	return b, "", nil
}

var acctRe = regexp.MustCompile(`"acct"<blob>="([^"]*)"`)

// keychainAccount reads the account field of the Claude Code credentials item
// so we can update (not duplicate) it on write-back.
func keychainAccount(service string) string {
	out, err := exec.Command("security", "find-generic-password",
		"-s", service).CombinedOutput()
	if err != nil {
		return ""
	}
	if m := acctRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

func writeClaudeBlob(blob []byte, account string) error {
	if claudeKeychainEnabled {
		args := []string{"add-generic-password", "-U", "-s", claudeKeychainService(), "-w", string(blob)}
		if account != "" {
			args = append(args, "-a", account)
		}
		return exec.Command("security", args...).Run()
	}
	path, err := claudeCredentialsPath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, blob, 0o600)
}
