# Active-session detection (hooks)

**English** | [中文](hooks.zh-CN.md)

Back to the [README](../README.md) · [Docs index](README.md).

At a window reset, `watch` avoids pinging while you're actively working — that
turn would start the next window on its own. This relies on **CLI hooks**, which
the install script sets up for you. If they aren't installed, Claude falls back
to Claude Code's own session list (`claude agents --json`, which marks a session
`busy` mid-turn), and Codex to its own session log: a rollout under
`~/.codex/sessions` written in the last 10 minutes whose last `task_started` has
no `task_complete` or `turn_aborted` after it. `limitping` never guesses from
the process list.

The install script runs this automatically; to (re)install manually:

```sh
limitping hooks install        # both providers (or: limitping hooks install claude)
```

This registers limitping's hooks in `~/.claude/settings.json` and
`~/.codex/hooks.json` (or under `CLAUDE_CONFIG_DIR` / `CODEX_HOME`; your existing
settings are preserved; a `.bak` backup is written). The hooks invoke the hidden
`limitping hook <provider>` command on `UserPromptSubmit` / `PreToolUse` /
`PostToolUse` / `Stop` / `SessionEnd`, plus the events that end a turn without
`Stop` — Claude's `StopFailure` (an API error such as a usage limit ended it)
and Codex's `Interrupt` — to record whether a session is mid-turn under
`~/.config/limitping/activity/`. `limitping upgrade` re-registers them, so a
release that relies on a new event picks it up.

```sh
limitping hooks status         # installed? current? (Codex) trusted?
```

`status` also flags hooks that another tool rewrote away or that predate the
current release.

> [!NOTE]
> Claude Code loads its hooks automatically — nothing to do there. **Codex**
> gates custom command hooks behind a one-time trust step: run `/hooks` inside
> Codex once to enable them (and again for hooks a newer release adds;
> `limitping hooks status` lists any Codex does not trust yet). Remove
> everything later with `limitping hooks uninstall` (also done automatically by
> `limitping uninstall`).
