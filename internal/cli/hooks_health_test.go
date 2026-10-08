package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wavever/CCLimitPing/internal/activity"
)

// isolatedHookConfigs points both CLIs' configs and limitping's own state at
// temp dirs, returning the Claude settings path.
func isolatedHookConfigs(t *testing.T) string {
	t.Helper()
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return filepath.Join(claude, "settings.json")
}

func TestCheckHooksStates(t *testing.T) {
	settings := isolatedHookConfigs(t)
	if state, _ := checkHooks("claude"); state != hooksOff {
		t.Fatalf("fresh state = %v, want off", state)
	}

	if err := runHooks(&strings.Builder{}, "claude", true); err != nil {
		t.Fatal(err)
	}
	if state, path := checkHooks("claude"); state != hooksOK || path != settings {
		t.Fatalf("after install = %v at %s, want ok at %s", state, path, settings)
	}

	// Another tool rewrites settings.json without our entries.
	if err := os.WriteFile(settings, []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, _ := checkHooks("claude"); state != hooksMissing {
		t.Fatalf("after a rewrite = %v, want missing", state)
	}

	// An install from an older release: some events only.
	binPath, _ := os.Executable()
	_, spec, _ := providerHookSpec("claude", binPath)
	spec.events = spec.events[:2]
	if _, err := applyHooks(settings, spec, true); err != nil {
		t.Fatal(err)
	}
	if state, _ := checkHooks("claude"); state != hooksOutdated {
		t.Fatalf("partial install = %v, want outdated", state)
	}
	if advice := hooksAdvice(enText, "claude"); !strings.Contains(advice, "limitping hooks install claude") {
		t.Fatalf("advice = %q", advice)
	}
}

func TestRefreshHooksOnlyTouchesEnabledProviders(t *testing.T) {
	settings := isolatedHookConfigs(t)
	codexHooks := filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json")

	// Claude enabled but outdated; Codex never installed.
	if err := activity.SetEnabled("claude", true); err != nil {
		t.Fatal(err)
	}
	if err := refreshHooks(&strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if state, _ := checkHooks("claude"); state != hooksOK {
		t.Fatalf("claude after refresh = %v, want ok", state)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(codexHooks); !os.IsNotExist(err) {
		t.Fatal("refresh installed hooks for a provider that never had them")
	}
}
