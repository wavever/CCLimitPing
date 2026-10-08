package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeRefreshEndpoint swaps authHTTPClient for one that answers the OAuth
// token endpoint with the given handler. No real network is reachable.
func fakeRefreshEndpoint(t *testing.T, handler roundTripFunc) {
	t.Helper()
	old := authHTTPClient
	authHTTPClient = &http.Client{Transport: handler}
	t.Cleanup(func() { authHTTPClient = old })
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

// --- Codex ---

func writeCodexAuth(t *testing.T, contents string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "auth.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCodexTokenLoadsFromAuthJSON(t *testing.T) {
	writeCodexAuth(t, `{"tokens":{"access_token":"at-1","refresh_token":"rt-1","account_id":"acct-1"}}`)

	a := NewCodexAuth()
	tok, err := a.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "at-1" {
		t.Fatalf("token = %q, want at-1", tok)
	}
	id, err := a.AccountID(context.Background())
	if err != nil || id != "acct-1" {
		t.Fatalf("AccountID = %q, %v, want acct-1", id, err)
	}
}

func TestCodexTokenErrors(t *testing.T) {
	cases := map[string]string{
		"no tokens object": `{"OPENAI_API_KEY":"sk-x"}`,
		"no access token":  `{"tokens":{"refresh_token":"rt-1"}}`,
		"invalid JSON":     `{not json`,
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			writeCodexAuth(t, contents)
			if _, err := NewCodexAuth().Token(context.Background()); err == nil {
				t.Fatalf("Token accepted %q", contents)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		t.Setenv("CODEX_HOME", t.TempDir())
		if _, err := NewCodexAuth().Token(context.Background()); err == nil {
			t.Fatal("Token succeeded without auth.json")
		}
	})
}

func TestCodexRefreshRotatesAndPersists(t *testing.T) {
	path := writeCodexAuth(t, `{
		"OPENAI_API_KEY": "keep-me",
		"tokens": {"access_token": "at-old", "refresh_token": "rt-old", "account_id": "acct-1"}
	}`)

	fakeRefreshEndpoint(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != codexTokenEndpoint {
			t.Fatalf("refresh URL = %q", req.URL.String())
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decoding refresh request: %v", err)
		}
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "rt-old" {
			t.Fatalf("refresh request = %v", body)
		}
		return jsonResponse(200, `{"access_token":"at-new","refresh_token":"rt-new","id_token":"id-new"}`), nil
	})

	a := NewCodexAuth()
	tok, err := a.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok != "at-new" {
		t.Fatalf("refreshed token = %q, want at-new", tok)
	}

	// The rotated tokens must be written back for the Codex CLI, preserving
	// unrelated fields.
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatalf("auth.json after refresh is invalid JSON: %v", err)
	}
	if raw["OPENAI_API_KEY"] != "keep-me" {
		t.Fatalf("unrelated field lost on write-back: %v", raw)
	}
	tokens := raw["tokens"].(map[string]any)
	if tokens["access_token"] != "at-new" || tokens["refresh_token"] != "rt-new" || tokens["id_token"] != "id-new" {
		t.Fatalf("persisted tokens = %v", tokens)
	}
	if raw["last_refresh"] == nil {
		t.Fatal("last_refresh not recorded")
	}
}

func TestCodexRefreshFailures(t *testing.T) {
	t.Run("HTTP error status", func(t *testing.T) {
		writeCodexAuth(t, `{"tokens":{"access_token":"at","refresh_token":"rt"}}`)
		fakeRefreshEndpoint(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(500, `{}`), nil
		})
		if _, err := NewCodexAuth().Refresh(context.Background()); err == nil {
			t.Fatal("Refresh accepted HTTP 500")
		}
	})
	t.Run("empty access token", func(t *testing.T) {
		writeCodexAuth(t, `{"tokens":{"access_token":"at","refresh_token":"rt"}}`)
		fakeRefreshEndpoint(t, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"access_token":""}`), nil
		})
		if _, err := NewCodexAuth().Refresh(context.Background()); err == nil {
			t.Fatal("Refresh accepted an empty access_token")
		}
	})
	t.Run("no refresh token", func(t *testing.T) {
		writeCodexAuth(t, `{"tokens":{"access_token":"at"}}`)
		fakeRefreshEndpoint(t, func(*http.Request) (*http.Response, error) {
			t.Fatal("refresh request sent without a refresh token")
			return nil, nil
		})
		if _, err := NewCodexAuth().Refresh(context.Background()); err == nil {
			t.Fatal("Refresh succeeded without a refresh token")
		}
	})
}

// --- Claude ---

// writeClaudeCreds disables the Keychain path and installs a credentials file
// under a temp HOME, so tests never touch the real store on any OS.
func writeClaudeCreds(t *testing.T, contents string) string {
	t.Helper()
	old := claudeKeychainEnabled
	claudeKeychainEnabled = false
	t.Cleanup(func() { claudeKeychainEnabled = old })

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".credentials.json")
	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestClaudeTokenLoadsWrappedShape(t *testing.T) {
	writeClaudeCreds(t, `{"claudeAiOauth":{"accessToken":"at-1","refreshToken":"rt-1","scopes":["user:profile"]}}`)

	tok, err := NewClaudeAuth().Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "at-1" {
		t.Fatalf("token = %q, want at-1", tok)
	}
}

func TestClaudeTokenLoadsFlatShape(t *testing.T) {
	writeClaudeCreds(t, `{"accessToken":"at-flat","refreshToken":"rt-flat"}`)

	tok, err := NewClaudeAuth().Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "at-flat" {
		t.Fatalf("token = %q, want at-flat", tok)
	}
}

func TestClaudeTokenErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		writeClaudeCreds(t, "")
		if _, err := NewClaudeAuth().Token(context.Background()); err == nil {
			t.Fatal("Token succeeded without credentials")
		}
	})
	t.Run("no access token", func(t *testing.T) {
		writeClaudeCreds(t, `{"claudeAiOauth":{"refreshToken":"rt-1"}}`)
		if _, err := NewClaudeAuth().Token(context.Background()); err == nil {
			t.Fatal("Token succeeded without accessToken")
		}
	})
}

func TestClaudeRefreshRotatesAndPersists(t *testing.T) {
	path := writeClaudeCreds(t, `{"claudeAiOauth":{"accessToken":"at-old","refreshToken":"rt-old","scopes":["user:profile"]}}`)

	fakeRefreshEndpoint(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != claudeTokenEndpoint {
			t.Fatalf("refresh URL = %q", req.URL.String())
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decoding refresh request: %v", err)
		}
		if body["refresh_token"] != "rt-old" || body["client_id"] != claudeOAuthClientID {
			t.Fatalf("refresh request = %v", body)
		}
		return jsonResponse(200, `{"access_token":"at-new","refresh_token":"rt-new","expires_in":3600}`), nil
	})

	a := NewClaudeAuth()
	tok, err := a.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok != "at-new" {
		t.Fatalf("refreshed token = %q, want at-new", tok)
	}

	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(blob, &raw); err != nil {
		t.Fatalf("credentials after refresh are invalid JSON: %v", err)
	}
	wrapper, _ := raw["claudeAiOauth"].(map[string]any)
	if wrapper == nil {
		t.Fatalf("write-back lost the claudeAiOauth wrapper: %v", raw)
	}
	if wrapper["accessToken"] != "at-new" || wrapper["refreshToken"] != "rt-new" {
		t.Fatalf("persisted tokens = %v", wrapper)
	}
	if _, ok := wrapper["scopes"]; !ok {
		t.Fatalf("unrelated field lost on write-back: %v", wrapper)
	}
	if _, ok := wrapper["expiresAt"]; !ok {
		t.Fatalf("expiresAt not recorded: %v", wrapper)
	}
}

func TestClaudeRefreshFailsWithoutRefreshToken(t *testing.T) {
	writeClaudeCreds(t, `{"claudeAiOauth":{"accessToken":"at-only"}}`)
	fakeRefreshEndpoint(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("refresh request sent without a refresh token")
		return nil, nil
	})
	if _, err := NewClaudeAuth().Refresh(context.Background()); err == nil {
		t.Fatal("Refresh succeeded without a refresh token")
	}
}

func TestClaudeKeychainServiceFollowsConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	os.Unsetenv("CLAUDE_SECURESTORAGE_CONFIG_DIR")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := claudeKeychainService(); got != "Claude Code-credentials" {
		t.Fatalf("default service = %q", got)
	}

	// Claude Code names a non-default config dir's item after the first 8 hex
	// digits of the dir's SHA-256, so two installs never share a login.
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/claude-work")
	if got := claudeKeychainService(); got != "Claude Code-credentials-"+sha8("/tmp/claude-work") {
		t.Fatalf("config-dir service = %q", got)
	}
	if path, _ := claudeCredentialsPath(); path != filepath.Join("/tmp/claude-work", ".credentials.json") {
		t.Fatalf("credentials path = %q", path)
	}

	// The secure-storage override wins, and an empty one means the default.
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "/tmp/claude-secure")
	if got := claudeKeychainService(); got != "Claude Code-credentials-"+sha8("/tmp/claude-secure") {
		t.Fatalf("secure-storage service = %q", got)
	}
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	if got := claudeKeychainService(); got != "Claude Code-credentials" {
		t.Fatalf("empty secure-storage override service = %q", got)
	}
}

func sha8(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}
