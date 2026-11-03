# How it works

**English** | [中文](how-it-works.zh-CN.md)

[Docs index](README.md) · [Project README](../README.md)

Two cleanly separated jobs:

| Job | Mechanism | Cost |
|-----|-----------|------|
| **Trigger** a new window | the official CLI (interactive Claude Code / headless `codex exec`) | a tiny slice of quota (this is the point) |
| **Read** usage & reset times | zero-quota usage endpoints (the same ones CodexBar / community plugins use) | none — never starts a window |

When `watch` sees a 5h window has reset, it first checks whether a Claude/Codex
session is actively mid-turn. If one is, `limitping` waits and re-reads usage
instead of sending its own ping, because that session's next model request will
start the new window naturally. This check relies on the
[CLI hooks](hooks.md#active-session-detection-hooks) (installed automatically by the
install script). Without them, Claude Code's own session list
(`claude agents --json`) answers for Claude, and Codex's own session log
(a rollout under `~/.codex/sessions` whose last turn is still open) answers
for Codex.

- **Claude**: reads `GET https://api.anthropic.com/api/oauth/usage` using the
  OAuth token from the macOS Keychain (`Claude Code-credentials`) or
  `~/.claude/.credentials.json` — or wherever `CLAUDE_CONFIG_DIR` points, as
  Claude Code itself does. Triggering uses a TTY-backed interactive
  `claude "<prompt>"` session, so it continues to start the Claude
  subscription-backed window after the headless print command moves to Agent
  SDK/API credits. The ping is kept as bare as the interactive CLI allows: it
  runs under its own `--session-id` and its transcript is deleted afterwards
  (pings used to pile up as "." conversations in `claude --resume`), with your
  hooks off (`disableAllHooks`), no MCP servers (`--strict-mcp-config`), no
  tools (`--tools ""`), no CLAUDE.md and nothing added to your prompt history
  — each flag only when the installed Claude Code supports it. The ping runs in
  the directory `watch` was started from when Claude Code trusts it; otherwise
  (the home directory, say, whose trust Claude Code does not keep) it borrows a
  directory Claude Code does trust and that has no project settings, so its
  workspace-trust prompt never blocks it — and only when the transcript can be
  deleted, so nothing is left behind there. If the usage endpoint returns an ambiguous 429, limitping
  uses the free token-counting endpoint (which does not create a Message) to
  distinguish a real endpoint throttle from Claude Code subscription access
  being disabled.
- **Codex**: reads `GET https://chatgpt.com/backend-api/wham/usage` using the
  OAuth token from `~/.codex/auth.json`. Triggering runs
  `codex exec --ephemeral --json "<prompt>"`. `--ephemeral` is the reason the
  ping is headless: the interactive CLI cannot skip persisting a session, so
  every ping used to leave an "ok" conversation behind in `codex resume` and in
  the Codex Desktop thread list. `--json` makes the ping verifiable — its
  `turn.completed` event is the only local proof that a billable request went
  out, and it is where the reported token count and cost come from. The ping
  also runs with `--disable hooks` and `--sandbox read-only`: your hooks have no
  business firing for a synthetic session (it must not register itself as an
  active Codex session either), and nothing reviews what the model does on this
  path, unlike an interactive session with you at the keys.

Claude/Codex tokens are reused from the official tools (no separate login) and
refreshed on 401.
