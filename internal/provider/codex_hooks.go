package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// CodexHook is one hook handler as Codex itself resolved it, including whether
// it will actually run: Codex only runs a command hook the user has trusted,
// and an edit to it after that ("modified") needs trusting again.
type CodexHook struct {
	EventName   string `json:"eventName"`
	Command     string `json:"command"`
	SourcePath  string `json:"sourcePath"`
	TrustStatus string `json:"trustStatus"`
}

// Runs reports whether Codex will run the hook.
func (h CodexHook) Runs() bool { return h.TrustStatus == "trusted" || h.TrustStatus == "managed" }

// CodexHooks asks Codex's app-server which hooks it has loaded for the user,
// with their trust state, so the answer is Codex's own rather than a guess at
// how it hashes hooks.json.
func CodexHooks(ctx context.Context) ([]CodexHook, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	result, err := codexAppServerCall(ctx, "hooks/list", map[string]any{"cwds": []string{home}})
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			Hooks []CodexHook `json:"hooks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return nil, fmt.Errorf("codex hooks/list: parsing response: %w", err)
	}
	var hooks []CodexHook
	for _, entry := range r.Data {
		hooks = append(hooks, entry.Hooks...)
	}
	return hooks, nil
}
