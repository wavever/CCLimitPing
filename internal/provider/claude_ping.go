package provider

import (
	"context"
	"crypto/rand"
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
//     directory watch was started in out of the request.
//
// --safe-mode would cover most of this in one flag, but it also drops settings
// a ping may need (an env-configured proxy, for one), so the narrower switches
// are used instead. Each flag is only passed when the installed Claude Code
// lists it, so an older CLI keeps working, merely without the isolation.

// claudePingEnv is added to the ping's environment.
const claudePingEnv = "CLAUDE_CODE_DISABLE_CLAUDE_MDS=1"

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
// unique by construction — is looked up across all of them.
func removeClaudePingTranscript(sessionID string) {
	if sessionID == "" {
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
