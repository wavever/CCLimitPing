package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wavever/CCLimitPing/internal/config"
)

// claudeTrustFixture points Claude Code's global config at a temp file that
// trusts the given directories, and moves into a directory it does not trust.
func claudeTrustFixture(t *testing.T, trusted ...string) (cwd string) {
	t.Helper()
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	projects := map[string]any{}
	for _, dir := range trusted {
		projects[dir] = map[string]any{"hasTrustDialogAccepted": true}
	}
	data, err := json.Marshal(map[string]any{"projects": projects})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, ".claude.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	return cwd
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClaudePingDirStaysInATrustedWorkingDirectory(t *testing.T) {
	other := t.TempDir()
	cwd := claudeTrustFixture(t, other)
	// Trust the working directory as well, now that its path is known.
	claudeTrustFixtureAdd(t, cwd)
	if dir, borrowed := claudePingDir(true); borrowed || dir != "" {
		t.Fatalf("claudePingDir = %q, %v; want the working directory kept", dir, borrowed)
	}
}

func TestClaudePingDirBorrowsATrustedDirectory(t *testing.T) {
	base := t.TempDir()
	gone := filepath.Join(base, "a-deleted")
	withSettings := filepath.Join(base, "b-with-settings")
	plain := filepath.Join(base, "c-plain")
	mkdirs(t, filepath.Join(withSettings, ".claude"), plain)
	if err := os.WriteFile(filepath.Join(withSettings, ".claude", "settings.local.json"), []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://proxy"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeTrustFixture(t, gone, withSettings, plain)

	dir, borrowed := claudePingDir(true)
	if !borrowed || dir != plain {
		t.Fatalf("claudePingDir = %q, %v; want %q: a missing directory and one whose settings could reroute the ping are passed over", dir, borrowed, plain)
	}
}

// Without its own session id the ping's transcript cannot be found again, so
// it would stay behind in somebody else's project.
func TestClaudePingDirNeverBorrowsWhenTheTranscriptCannotBeDeleted(t *testing.T) {
	plain := t.TempDir()
	claudeTrustFixture(t, plain)
	if dir, borrowed := claudePingDir(false); borrowed || dir != "" {
		t.Fatalf("claudePingDir = %q, %v; want no borrowing", dir, borrowed)
	}
}

func TestClaudePingDirWithNothingToBorrow(t *testing.T) {
	claudeTrustFixture(t)
	if dir, borrowed := claudePingDir(true); borrowed || dir != "" {
		t.Fatalf("claudePingDir = %q, %v; want the working directory", dir, borrowed)
	}
}

func TestClaudeTriggerShowsTheBorrowedDirectory(t *testing.T) {
	fakeClaudeHelp(t, claudeHelpWithIsolationFlags)
	plain := t.TempDir()
	claudeTrustFixture(t, plain)
	res, err := NewClaude(config.ProviderConfig{Prompt: "."}).Trigger(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Command, "cd "+plain+" && claude ") {
		t.Fatalf("command = %q, want it to say where the ping runs", res.Command)
	}
}

func TestRemoveClaudePingTranscriptRefusesAnythingButAUUID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	project := filepath.Join(dir, "projects", "-Users-me-work")
	keep := filepath.Join(project, "0b7ad3c4-1111-4222-8333-944455556666.jsonl")
	mkdirs(t, project)
	if err := os.WriteFile(keep, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "*", "0b7ad3c4-*", "../projects/-Users-me-work/0b7ad3c4-1111-4222-8333-944455556666"} {
		removeClaudePingTranscript(id)
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("id %q removed a conversation that was not the ping's: %v", id, err)
		}
	}
}

// claudeTrustFixtureAdd marks one more directory trusted in the fixture.
func claudeTrustFixtureAdd(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["projects"][dir] = map[string]any{"hasTrustDialogAccepted": true}
	if data, err = json.Marshal(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
