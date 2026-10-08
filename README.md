<p align="center">
  <img src="assets/icon.png" alt="CCLimitPing icon" width="160">
</p>

# CCLimitPing (`limitping`)

**English** | [中文](README.zh-CN.md)

[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![CI](https://github.com/wavever/CCLimitPing/actions/workflows/ci.yml/badge.svg)](https://github.com/wavever/CCLimitPing/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/wavever/CCLimitPing?include_prereleases&sort=semver)](https://github.com/wavever/CCLimitPing/releases)
![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)

Start the next **Claude Code** or **Codex** rate-limit window the moment the
previous one resets.

Claude Code and Codex subscription limits run on **5-hour rolling windows**
(plus a weekly cap). A fresh 5h window does not start just because the previous
one reset; it starts when you send the first billable request. If that
happens hours later, the gap is wasted and your window schedule drifts.

`limitping` watches the reset time and sends one tiny request through the
official provider CLI right after rollover. Run it once, keep `watch` in the
foreground, or start a detached `bg` watcher that keeps your window chain alive
after the terminal closes.

```
claude  ✓ pinged (6.6s)
codex   ✓ pinged (14s, 19,426 tok (in 19,414 / out 12), $0.0023)
```

## Highlights

- Keeps 5h windows continuous by pinging as soon as a reset is safely available.
- Runs the way you do: one-shot `ping`, foreground `watch`, or detached
  `bg start` with `bg status`, `bg logs -f`, and `bg stop`.
- Shows 5h and weekly usage, reset countdowns, and background watcher state from
  read-only usage endpoints.
- Counts today's tokens and what they would have cost at API rates, read from the
  CLIs' own local session transcripts — the number the percentages never show.
- Triggers Claude Code and Codex through their official CLIs using your existing
  logged-in credentials.
- Detects active Claude/Codex turns via CLI hooks, so a ping never interrupts a
  session that is already about to start the window itself.
- Auto-resumes parked tasks: `limitping continue <provider>` proxies the
  official CLI and types your continue message the moment the 5h limit
  recovers, so an overnight task doesn't sit at the limit until morning.
- Spends reset credits before they lapse — Codex reset credits and Claude reset
  cards (the one-off usage-limit resets Anthropic hands out, e.g. at a model
  launch): `status` lists them, `limitping redeem` cashes one in by hand, and
  `auto_redeem = true` lets `watch` / `continue` spend one on its own once it is
  close to expiring — a banked reset is worth nothing after it expires. Off by
  default, because redeeming is irreversible.
- Types short: every command also works as `lmp` (e.g. `lmp s`, `lmp w`).
- Includes dry-run modes, weekly-limit guards, reset buffers, cheap-model
  defaults, macOS notifications, local config, and no telemetry.

## Quick start

```sh
curl -fsSL https://raw.githubusercontent.com/wavever/CCLimitPing/main/install.sh | sh
limitping config init
limitping status
limitping ping --dry-run
limitping watch                # foreground, low-power (Ctrl-C to stop)
# ...or run it in the background, freeing your terminal:
limitping bg start
limitping bg status
limitping bg logs -f
```

Use dry-run first if you want to inspect what would happen without consuming
provider quota: `limitping ping --dry-run`, `limitping watch --dry-run`, or
`limitping bg start --dry-run`.

## Supported providers

| Provider | Read usage (zero-quota) | Trigger | Auth |
|---|---|---|---|
| **Claude Code** | `…/api/oauth/usage` | interactive Claude Code CLI | OAuth (Keychain / `~/.claude`) |
| **Codex** | `…/backend-api/wham/usage` | `codex exec --ephemeral` | OAuth (`~/.codex/auth.json`) |

## How it works

Two cleanly separated jobs:

| Job | Mechanism | Cost |
|-----|-----------|------|
| **Trigger** a new window | the official CLI (interactive Claude Code / headless `codex exec`) | a tiny slice of quota (this is the point) |
| **Read** usage & reset times | zero-quota usage endpoints (the same ones CodexBar / community plugins use) | none — never starts a window |

When `watch` sees a 5h window has reset, it first checks whether a Claude/Codex
session is actively mid-turn. If one is, `limitping` waits and re-reads usage
instead of sending its own ping, because that session's next model request will
start the new window naturally. This check relies on the
[CLI hooks](#active-session-detection-hooks) (installed automatically by the
install script); without them, `limitping` skips the check and pings as soon as
the window resets.

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
  tools (`--tools ""`) and no CLAUDE.md — each flag only when the installed
  Claude Code supports it. If the usage endpoint returns an ambiguous 429, limitping
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

## Install

`limitping` ships as a single self-contained binary — **no Go required**.

**One-line script** (macOS / Linux):

```sh
curl -fsSL https://raw.githubusercontent.com/wavever/CCLimitPing/main/install.sh | sh
```

Downloads the right prebuilt binary from the
[latest release](https://github.com/wavever/CCLimitPing/releases/latest) into
`/usr/local/bin` (or `~/.local/bin`). Override with `LIMITPING_INSTALL_DIR`.

**Upgrade** — replace the installed binary with the latest release:

```sh
limitping upgrade
```

`upgrade` checks first and says so if you are already current; `--force`
reinstalls anyway. `status`, `ping`, `bg status` and `continue` also announce a
new release before their own output, once per release:

```
✨ Update available!  0.9.0 -> 0.10.0
   Release notes: https://github.com/wavever/CCLimitPing/releases/latest

     1. Update now (runs `limitping upgrade`)
   ❯ 2. Skip
     3. Skip until next version

   ↑/↓ move · Enter confirm · Esc skip
```

Move with the arrow keys and confirm with Enter, the way the provider CLIs do
it; the number keys still pick an option outright. The cursor starts on Skip
because the notice interrupts the command you actually ran, so Enter, Esc and
Ctrl-C all leave everything as it was. Option 3 records the release in
`~/.config/limitping/version.json` and stays quiet until the next one. The check
runs at most once a day, never blocks for more than two seconds, and is skipped
entirely without an interactive terminal — so `--json`, the `hook` callback and
background watchers stay silent.

Aliases: `limitping up`, `limitping update`.

**Uninstall** — remove the installed binary plus config/cache:

```sh
limitping uninstall
```

Aliases: `limitping rm`, `limitping remove`.

Use `limitping uninstall --keep-config` to preserve `~/.config/limitping` (or
`$XDG_CONFIG_HOME/limitping`).

**Manual download** — grab the archive for your platform from the
[Releases](https://github.com/wavever/CCLimitPing/releases) page (`.tar.gz` for
macOS/Linux, `.zip` for Windows):

```sh
tar -xzf limitping_darwin_arm64.tar.gz
sudo mv limitping /usr/local/bin/
```

**Homebrew** (macOS / Linux) — `brew install wavever/tap/limitping`
_(works once the Homebrew tap is set up — see `.goreleaser.yaml`)._

**From source** (developers, needs Go 1.25+):

```sh
go install github.com/wavever/CCLimitPing/cmd/limitping@latest
# or, from a clone:
go build -o bin/limitping ./cmd/limitping
```

Each provider you enable needs its own credentials: the `claude` / `codex` CLIs
logged in.

## Usage

```sh
limitping config init          # write ~/.config/limitping/config.toml
limitping status               # 5h/weekly % + reset countdowns + today's tokens (alias: s)
limitping status --json        # machine-readable JSON for each provider
limitping status -v            # also print the per-model breakdown and raw JSON
limitping ping                 # trigger all enabled providers now (alias: p)
limitping ping claude          # Claude only
limitping ping codex           # Codex only
limitping ping --dry-run       # show the commands without sending
limitping watch                # foreground daemon: ping each window at reset (alias: w)
limitping watch claude         # watch only one provider (claude|codex)
limitping watch --live         # optional live heartbeat/status line
limitping watch --dry-run      # log when pings would fire, without sending
limitping schedule codex --at 05:00 --at 13:00  # ping at daily local times
limitping schedule --every 5h  # ping on a fixed interval instead of reset time
limitping redeem --dry-run     # show which Codex reset credit would be spent
limitping redeem               # spend it now (irreversible)
limitping continue codex       # proxy the CLI; auto-resume the task on 5h recovery
limitping continue codex --yolo             # flags after the provider pass through
limitping continue claude --dangerously-skip-permissions
limitping bg start             # run watch in the background, freeing the terminal
limitping bg status            # running? + each watched provider's usage (alias: limitping bg)
limitping bg logs -f           # follow the background watcher's log
limitping bg stop              # stop the background watcher
limitping hooks install        # install active-session detection hooks (claude|codex|all)
limitping hooks uninstall      # remove those hooks
limitping hooks status         # check they are installed, current and (Codex) trusted
limitping version              # print the version (aliases: v, ver)
limitping upgrade              # update to the latest GitHub release (aliases: up, update)
limitping uninstall            # remove limitping plus config/cache (aliases: rm, remove)
```

Short aliases are also available for config commands: `limitping c i` for
`config init` and `limitping c p` for `config path`.

### Command aliases

`limitping --help` lists aliases inline, for example `ping, p`, and its own
`Aliases:` line shows both binary names so either one is discoverable from the
other.

The binary itself has a short name too: the installer symlinks `lmp` next to
`limitping`, so `lmp status`, `lmp w`, and `limitping status` are the same
command. The installer skips the link if `lmp` already exists or resolves to
another command on your PATH — a symlink in `/usr/local/bin` shadows anything
it collides with. (Building from source? `ln -s limitping /usr/local/bin/lmp`.)

| Command | Aliases |
| --- | --- |
| `status` | `s`, `stat` |
| `ping` | `p` |
| `watch` | `w` |
| `schedule` | `sched` |
| `redeem` | `r` |
| `background` | `bg` |
| `config` | `c`, `cfg` |
| `config init` | `c i` |
| `config path` | `c p` |
| `version` | `v`, `ver` |
| `upgrade` | `up`, `update` |
| `uninstall` | `rm`, `remove` |

`ping` shows the exact command and a live timer (a spinner on a terminal). The
Codex ping reports the turn's tokens and an equivalent API cost, read from
`codex exec --json`; Claude's interactive trigger session exposes no reliable
machine-readable per-ping usage, so it shows elapsed time only:

```
claude  → claude --model haiku .
claude  ✓ pinged (6.6s)
codex   → codex exec --ephemeral --json --skip-git-repo-check --disable hooks --sandbox read-only -c model_reasoning_effort=low -m gpt-5.6-luna ok
codex   ✓ pinged (14s, 19,426 tok (in 19,414 / out 12), $0.0023)
```

`ping` and the `watch` log always name the model. In the rare case limitping
cannot pick one — no catalog on disk, or no recognizable budget tier in it — the
Codex CLI chooses instead, and the model is reported alongside the command so
you still see what the ping spent quota on:

```
codex   → codex exec --ephemeral --json … -c model_reasoning_effort=low ok  (model: gpt-5.6-sol)
```

A ping ends by printing the same window view `status` gives, for the providers
it pinged — a ping is far too small to move the used percentage, so its own
output cannot show whether a window started. Reading usage costs nothing and
never starts a window. `--dry-run` skips it: nothing was sent, so there is no
new state to report.

Example `status`:

```
claude
  5h     [█████░░░░░]  51.0% used      resets in 3h14m    (Sun 00:10 UTC+8)
  weekly [█████░░░░░]  54.0% used      resets in 7h04m    (Sun 04:00 UTC+8)
  today  55.0M tok  ≈ $41.03

codex (plus)
  5h     [██░░░░░░░░]  24.0% used      resets in 3h15m    (Sun 00:11 UTC+8)
  weekly [████░░░░░░]  37.0% used      resets in 111h57m  (Thu 12:53 UTC+8)
  today  20.5M tok  ≈ $10.06
  reset credits 1 reset available
    - available, granted Jun 17 17:38, expires Jul 17 17:38 UTC+8 (in 24d6h)
```

Text status defaults to **used** percentage. Set `usage_display = "remaining"` if
you prefer the same mental model as Codex's "Usage remaining" UI.

### Today's tokens and cost

The `today` line answers what the percentages cannot: how much this machine
actually consumed since local midnight, and what that would have cost at
published API rates — the value a subscription is returning. The usage endpoints
only ever report percentages, so the tokens are totalled from the transcripts
the CLIs already write to disk (`~/.claude/projects`, `~/.codex/sessions` —
honoring `$CLAUDE_CONFIG_DIR` / `$CODEX_HOME`), the same source ccusage and
CodexBar read, and priced with the [LiteLLM](https://github.com/BerriAI/litellm)
dataset limitping already caches for `ping`. Nothing is uploaded: the scan is
local, read-only, and runs alongside the usage fetch, so it costs no extra wall
time.

Two consequences worth knowing: it is a **local** view — sessions run on another
machine, or in the web app, leave no transcript here and are not counted — and
the cost is an **estimate**, since a subscription does not bill per token. A
model too new to be in the pricing dataset still has its tokens counted; it just
adds nothing to the dollar figure (`cost_complete: false` in JSON).

`status -v` breaks the day down by bucket and by model:

```
  today  55.0M tok  ≈ $41.03
         in 770 · cache 53.6M read / 1.1M write · out 300.6K
         claude-opus-5              54.9M tok  ≈ $40.95
         claude-haiku-4-5-20251001  67.4K tok  ≈ $0.09
```

The line is omitted entirely for a provider whose CLI has never run on this
machine — silence there is honest, where `0 tok` would claim a quiet day.

`status --json` returns the same data as a JSON array (one object per provider),
for scripts and dashboards. Progress chatter is suppressed so stdout stays a
single valid document; a provider that fails to read becomes
`{"provider": "...", "error": "..."}` and the command exits non-zero. Add `-v`
to embed each provider's raw response under `raw`.

`today` is omitted for a provider with no local transcripts at all; when
present, `cost_usd` is the API-rate estimate and `cost_complete` is false if a
model that ran had no published rates, making that figure a lower bound.

A window key (`five_hour` / `weekly`) is omitted when the provider does not
currently enforce that limit — e.g. OpenAI temporarily removed Codex's 5h
window on 2026-07-12, leaving only the weekly cap. Text mode prints
`not currently enforced` for such a window, and `watch` schedules its ping at
the weekly reset instead of every 5h.

An enforced limit that nothing has started yet is a different state: text mode
prints `(no active window)` and `resets_at` is absent from the JSON, because a
rolling window has no reset time until a request anchors it. Codex does not
report that state directly — it answers with a full-length window that slides
forward on every read (`reset_after_seconds` equal to `limit_window_seconds`),
i.e. when a window *would* end if you started one now. limitping normalizes that
away, so a reset time is only ever shown for a window that is really running.

```json
[
  {
    "provider": "codex",
    "plan": "plus",
    "five_hour": {
      "used_percent": 24,
      "remaining_percent": 76,
      "active": true,
      "resets_at": "2026-06-17T05:51:45+08:00",
      "remaining_seconds": 11700,
      "window_seconds": 18000
    },
    "weekly": {
      "used_percent": 37,
      "remaining_percent": 63,
      "active": true,
      "resets_at": "2026-06-24T00:51:45+08:00",
      "remaining_seconds": 403020,
      "window_seconds": 604800
    },
    "credits": { "has_credits": false, "unlimited": false, "balance": "0" },
    "reset_credits": {
      "available_count": 1,
      "credits": [
        {
          "status": "available",
          "granted_at": "2026-06-17T17:38:38Z",
          "expires_at": "2026-07-17T17:38:38Z"
        }
      ]
    },
    "today": {
      "date": "2026-06-17",
      "input_tokens": 674291,
      "cache_read_tokens": 19719552,
      "cache_creation_tokens": 0,
      "output_tokens": 81039,
      "total_tokens": 20474882,
      "cost_usd": 10.059257,
      "cost_complete": true,
      "models": [
        { "model": "gpt-5.6-sol", "total_tokens": 13774852, "cost_usd": 7.748941 },
        { "model": "gpt-5.6-terra", "total_tokens": 6700030, "cost_usd": 2.310316 }
      ]
    },
    "limit_reached": false,
    "fetched_at": "2026-06-17T01:00:43+08:00"
  }
]
```

## Configuration

`~/.config/limitping/config.toml` (honors `$XDG_CONFIG_HOME`):

```toml
weekly_threshold = 0.99   # skip pinging when weekly usage >= this (0..1), until weekly reset
reset_buffer     = "10s"  # wait this long after a reset before pinging (ensures rollover)
notify           = true   # macOS notifications on ping/skip/failure
usage_display    = "used" # text status: "used" or "remaining"

[claude]
enabled    = true
prompt     = "."
model      = "haiku"      # cheapest tier; triggering doesn't need a SOTA model
extra_args = []           # extra Claude CLI args; print/headless-only flags are ignored
align_start = ""          # optional RFC3339 anchor for the first window; empty = start ASAP
continue_prompt = "continue"  # message `continue` injects on 5h recovery; empty = "continue"

[codex]
enabled          = true
prompt           = "ok"
model            = ""     # empty = pick the cheapest model your plan offers
reasoning_effort = "low"  # "minimal" is rejected when web_search/image_gen tools are enabled
extra_args       = []     # extra Codex CLI args; exec-only flags such as --json are ignored
align_start      = ""
continue_prompt  = "continue"  # message `continue` injects on 5h recovery; empty = "continue"
```

Top-level keys:

- **`weekly_threshold`** — when the weekly window is at/above this, `watch` stops
  pinging and waits for the weekly reset (unless usable credits exist).
- **`reset_buffer`** — how long to wait after a window's reset time before
  pinging, so the window has definitely rolled over.
- **`usage_display`** — whether text `status` / `bg status` renders each window
  as used percentage or remaining percentage.
- **`align_start`** (per provider) — pin the phase of your windows: set to a
  future RFC3339 time to delay the very first ping until then; afterwards windows
  chain automatically every ~5h.

### Why a cheap model

Triggering a window doesn't depend on the model — **any** billable request starts
the 5h clock — so the ping uses each provider's cheapest model to eat the least of
your budget:

- **Claude → `haiku`**: also avoids the separate weekly Opus bucket.
- **Codex → empty, resolved per ping**: limitping reads the Codex CLI's own
  catalog (`~/.codex/models_cache.json`) and picks the cheapest model your plan
  offers — the budget tier OpenAI marks "Fast and affordable", never your
  working model, which is usually a far pricier tier. Because the choice is made
  at ping time it survives OpenAI retiring and adding models. Name a model to
  pin one instead; a pinned model that has since been retired is rejected with
  the current list rather than failing as an opaque server error at rollover.

Claude/Codex don't expose per-model prices at runtime (Anthropic's local
cost cache is empty; Codex's model cache has no price field), so the cheapest
model is a sensible default rather than a live price lookup. Override `model`
per provider if you prefer.

### Active-session detection (hooks)

At a window reset, `watch` avoids pinging while you're actively working — that
turn would start the next window on its own. This relies on **CLI hooks**, which
the install script sets up for you. If they aren't installed, `limitping` skips
the check entirely and pings right at reset (it never guesses from the process
list).

The install script runs this automatically; to (re)install manually:

```sh
limitping hooks install        # both providers (or: limitping hooks install claude)
```

This registers limitping's hooks in `~/.claude/settings.json` and
`~/.codex/hooks.json` (your existing settings are preserved; a `.bak` backup is
written). The hooks invoke the hidden `limitping hook <provider>` command on
`UserPromptSubmit` / `PreToolUse` / `PostToolUse` / `Stop` (Claude also
`SessionEnd`) to record whether a session is mid-turn under
`~/.config/limitping/activity/`.

> [!NOTE]
> Claude Code loads its hooks automatically — nothing to do there. **Codex**
> gates custom command hooks behind a one-time trust step: run `/hooks` inside
> Codex once to enable them. Remove everything later with
> `limitping hooks uninstall` (also done automatically by `limitping uninstall`).

## Scheduled pings

Use `schedule` when you want **wall-clock pings** instead of reset-aligned
window chaining. It keeps running in the foreground and fires `ping` at the next
configured interval or daily local time:

```sh
limitping schedule codex --at 05:00
limitping schedule codex --at 05:00 --at 13:00 --at 21:00
limitping schedule --at 05:00,13:00,21:00
limitping schedule codex --every 5h --dry-run
```

You can combine `--every` and `--at`; whichever next occurrence comes first is
used. `--at` values are daily local times in `HH:MM` or `HH:MM:SS` form.

## Run `watch` in the background

`watch` runs in the foreground. To free your terminal, run it as a detached
background process with the built-in `bg` command:

```sh
limitping bg start          # start watch detached from the terminal
limitping bg status         # running? pid, uptime, log + each provider's usage (alias: limitping bg)
limitping bg logs -f        # follow the watcher's log (-n N for last N lines)
limitping bg stop           # stop it
```

`watch` defaults to low-power log output. Add `--live` if you want a foreground
heartbeat/status line. `bg start` takes the same optional `[provider]` argument
and `--dry-run` flag as `watch`. Only one watcher (foreground or background) runs
at a time, and background output is written to
`~/.config/limitping/bg.log` (honors `$XDG_CONFIG_HOME`). The process detaches
into its own session, so it survives the shell closing — but it does **not**
restart on reboot.

For **start-at-login** on macOS, use a `launchd` agent instead. Create
`~/Library/LaunchAgents/com.limitping.watch.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.limitping.watch</string>
  <key>ProgramArguments</key>
  <array>
    <string>/ABSOLUTE/PATH/TO/limitping</string>
    <string>watch</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/limitping.log</string>
  <key>StandardErrorPath</key><string>/tmp/limitping.err</string>
</dict>
</plist>
```

```sh
launchctl load ~/Library/LaunchAgents/com.limitping.watch.plist
```

## Auto-continue a parked task

`watch` and `bg` keep your window chain warm, but they don't resume a task that
has already stalled at the 5h limit. `limitping continue <provider>` does: it
launches the provider's real interactive CLI through a PTY and passes your
terminal straight through, so you drive Codex / Claude Code exactly as usual.
In the background it polls usage and, the moment the 5h limit recovers after
being hit, types your continue message into the session so a long task resumes
itself instead of sitting parked until you come back.

```sh
limitping continue codex                       # drive Codex as usual; auto-resume on recovery
limitping continue codex --yolo                # flags after the provider pass through verbatim
limitping continue claude --dangerously-skip-permissions
```

- The resume message is each provider's `continue_prompt` in config (default
  `"continue"`; set it to e.g. `"继续任务"`). Quit from inside the CLI to exit.
- It only injects on a genuine recovery edge: the 5h window was maxed (or the
  endpoint reported `limit_reached`, or the CLI printed a limit message) and has
  since clearly reset, and the weekly window isn't also exhausted (per
  `weekly_threshold`, credits included) — so it won't resume straight into the
  weekly wall.
- A diagnostic timeline is written to `~/.config/limitping/continue.log`.
- Unix only for now (needs a PTY); on Windows the command reports that it's
  unsupported.

## Cost & caveats

- See [PRIVACY.md](PRIVACY.md) for local data handling and network behavior.
- See [SECURITY.md](SECURITY.md) for vulnerability reporting and credential
  handling notes.
- Triggering **consumes a little quota** (~one ping per 5h ≈ 33/week). The ping
  uses a minimal prompt and low reasoning, so the cost is tiny but non-zero.
- The **usage endpoints are unofficial** and could change; they're read-only and
  isolated per provider for easy patching.
- macOS-first: Keychain reads and notifications are macOS-only. Codex
  `auth.json` is cross-platform; Claude on Linux uses
  `~/.claude/.credentials.json`; notifications are a no-op off macOS.

## Layout

```
cmd/limitping            CLI entry
internal/config          TOML config
internal/usage           normalized usage model
internal/auth            Claude (Keychain) + Codex (auth.json) tokens
internal/provider        per-provider ReadUsage (endpoint) + Trigger (CLI)
internal/activity        hook-based active-session state (shared by the hook cmd + scheduler)
internal/pricing         pricing helpers for providers that expose token usage
internal/spend           today's tokens/cost, read from the CLIs' local session transcripts
internal/scheduler       the watch engine (sleep-until-reset, weekly-respect, backoff)
internal/notify          macOS osascript notifications
internal/cli             cobra commands: status, ping, watch, schedule, continue, background, config, hooks, upgrade, uninstall, version
```

## Contributing

Issues and PRs are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) and
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Before submitting:

```sh
gofmt -l .        # should print nothing
go build ./...
go vet ./...
go test ./...
```

Providers are isolated in `internal/provider` behind a small `Provider`
interface (`ReadUsage` + `Trigger`), so adding a new provider is mostly
self-contained provider code plus wiring in `internal/cli` and `internal/config`.

**Releasing** is one command — push a tag, and GitHub Actions runs GoReleaser to
build the cross-platform binaries and publish a Release:

```sh
git tag v0.10.0 && git push origin v0.10.0
```

Nothing else needs editing. The tag is the only place a version is written down:
the release build stamps it in via `-ldflags`, and any other build derives its
version from the module's build info, so there is no constant to bump and
nothing that can drift from the tag. A local `go build` reports
`dev+<revision>` and never offers to upgrade itself.

Release notes are generated from the commit log, so **commit subjects are the
release notes** — write them as a line a user would want to read. There is no
hand-maintained changelog to keep in sync; published notes live on the
[Releases](https://github.com/wavever/CCLimitPing/releases) page.

## License

[MIT](LICENSE) © wavever
