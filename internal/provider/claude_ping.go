package provider

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wavever/CCLimitPing/internal/auth"
)

// A ping is a synthetic session, so it is kept as close to a bare request as
// the interactive CLI allows — the same reasoning that runs the Codex ping with
// --ephemeral and --disable hooks:
//
//   - --session-id names the session up front, so its transcript can be deleted
//     afterwards. The interactive CLI cannot skip persisting it, and every ping
//     used to leave a "." conversation behind in `claude --resume`.
//   - disableAllHooks keeps the user's hooks from firing for it — limitping's
//     own included, which would register the ping as an active session — and
//     promptSuggestionEnabled=false skips the extra request Claude Code makes
//     after a reply to suggest the next prompt.
//   - --strict-mcp-config (with no --mcp-config) starts no MCP servers, and
//     --tools "" sends no tool definitions: neither does anything for a ping
//     but slow it down and enlarge the request it bills.
//   - CLAUDE_CODE_DISABLE_CLAUDE_MDS keeps the CLAUDE.md files of whatever
//     directory the ping runs in out of the request, and
//     CLAUDE_CODE_SKIP_PROMPT_HISTORY keeps its "." out of the prompt history
//     (~/.claude/history.jsonl) that the up arrow recalls in that directory.
//
// --safe-mode would cover most of this in one flag, but it also drops settings
// a ping may need (an env-configured proxy, for one), so the narrower switches
// are used instead. Each flag is only passed when the installed Claude Code
// lists it, so an older CLI keeps working, merely without the isolation; an
// environment variable an older CLI does not know is simply ignored.

// claudePingEnv is added to the ping's environment.
var claudePingEnv = []string{
	"CLAUDE_CODE_DISABLE_CLAUDE_MDS=1",
	"CLAUDE_CODE_SKIP_PROMPT_HISTORY=1",
}

// claudePingDir picks the directory the ping runs in. Claude Code asks whether
// to trust a directory it has not been told to, and the ping cannot answer —
// so a watch started from an untrusted directory (the home directory, say,
// whose trust Claude Code does not keep) could never ping. The working
// directory is used when Claude Code trusts it outright; otherwise the ping
// borrows a directory that is.
//
// A borrowed directory is somebody's project, so the ping may only pass
// through it without a trace: canBorrow must say the ping's transcript will be
// deleted (it runs under its own --session-id), and a directory with project
// settings is passed over, since those could send the request somewhere other
// than the subscription whose window it is meant to start. Its CLAUDE.md,
// MCP servers and hooks are already kept out by the isolation above.
//
// Only an exact trust entry counts. Claude Code also extends a parent's trust
// to its subdirectories, but only within bounds (a repository's root) not
// worth reproducing; a directory trusted that way is merely borrowed for when
// it need not be. It returns "" when nothing better than the working directory
// is available, leaving the trust dialog to report the problem.
func claudePingDir(canBorrow bool) (dir string, borrowed bool) {
	trusted := claudeTrustedDirs()
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	if trusted[cwd] {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil && trusted[real] {
		return "", false
	}
	if !canBorrow {
		return "", false
	}
	candidates := make([]string, 0, len(trusted))
	for path, ok := range trusted {
		if ok {
			candidates = append(candidates, path)
		}
	}
	// Sorted so the same directory is borrowed every time.
	slices.Sort(candidates)
	for _, path := range candidates {
		if claudeBorrowable(path) {
			return path, true
		}
	}
	return "", false
}

// claudeTrustedDirs reads the directories Claude Code has been told to trust
// from its global config. Unreadable means none.
func claudeTrustedDirs() map[string]bool {
	path, err := auth.ClaudeGlobalConfigPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	trusted := make(map[string]bool, len(cfg.Projects))
	for dir, p := range cfg.Projects {
		if p.HasTrustDialogAccepted {
			trusted[dir] = true
		}
	}
	return trusted
}

// claudeBorrowable reports whether a trusted directory can host the ping: it
// still exists, and carries no project settings that could reroute it.
func claudeBorrowable(dir string) bool {
	if !filepath.IsAbs(dir) {
		return false
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		if _, err := os.Lstat(filepath.Join(dir, ".claude", name)); !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

// claudeHelp returns `claude --help`; tests swap it for a fixed text.
var claudeHelp = installedClaudeHelp

var (
	claudeHelpOnce sync.Once
	claudeHelpText string
)

func installedClaudeHelp() string {
	claudeHelpOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "claude", "--help").Output()
		if err == nil {
			claudeHelpText = string(out)
		}
	})
	return claudeHelpText
}

// claudeFlagSupported reports whether the installed CLI defines flag: whether
// one of the help's option lines (indented two spaces, possibly listing aliases
// first) declares it. A mention in another option's description — "--bare"
// wraps onto a line starting "--settings, --agents" — does not count.
func claudeFlagSupported(flag string) bool {
	re := regexp.MustCompile(`(?m)^ {2,4}(?:-[A-Za-z], )?(?:--[A-Za-z0-9-]+, )*` + regexp.QuoteMeta(flag) + `(?:[ =,]|$)`)
	return re.MatchString(claudeHelp())
}

// claudePingIsolationArgs returns the isolating flags the installed CLI
// supports, skipping any the user already passes in extra_args.
func claudePingIsolationArgs(sessionID string, extra []string) []string {
	userSets := func(flag string) bool {
		return slices.ContainsFunc(extra, func(a string) bool {
			f, _ := splitFlagValue(a)
			return f == flag
		})
	}
	var out []string
	add := func(flag string, values ...string) {
		if !userSets(flag) && claudeFlagSupported(flag) {
			out = append(out, flag)
			out = append(out, values...)
		}
	}
	add("--session-id", sessionID)
	add("--settings", `{"disableAllHooks":true,"promptSuggestionEnabled":false}`)
	add("--strict-mcp-config")
	add("--tools", "")
	return out
}

var claudeSessionIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// newClaudeSessionID returns a random (version 4) UUID, the form --session-id
// requires.
func newClaudeSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// removeClaudePingTranscript deletes what the ping's session left on disk: the
// transcript, and the per-session directory Claude Code keeps beside it for
// larger tool results. The project directory it sits in is named after the
// working directory by rules limitping does not reproduce, so the session id —
// unique by construction — is looked up across all of them. Anything but a
// UUID is refused outright: the id goes into a glob, and only the one this
// ping generated may ever match.
func removeClaudePingTranscript(sessionID string) {
	if !claudeSessionIDRE.MatchString(sessionID) {
		return
	}
	dir, err := auth.ClaudeConfigDir()
	if err != nil {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "projects", "*", sessionID+".jsonl"))
	for _, path := range matches {
		_ = os.Remove(path)
		_ = os.RemoveAll(strings.TrimSuffix(path, ".jsonl"))
	}
}
