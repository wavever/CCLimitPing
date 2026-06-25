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

Start the next **Claude Code**, **Codex**, or **Spark** rate-limit window the
moment the previous one resets.

Claude Code, Codex, and Spark subscription limits run on **5-hour rolling
windows** (plus a weekly cap). A fresh 5h window does not start just because the
previous one reset; it starts when you send the first billable request. If that
happens hours later, the gap is wasted and your window schedule drifts.

`limitping` watches the reset time and sends one tiny request through the
official provider CLI right after rollover. Run it once, keep `watch` in the
foreground, or start a detached `bg` watcher that keeps your window chain alive
after the terminal closes.

```
claude  ✓ pinged (6.6s)
codex   ✓ pinged (13.6s)
spark   ✓ pinged (12.4s)
```

## Highlights

- Keeps 5h windows continuous by pinging as soon as a reset is safely available.
- Runs the way you do: one-shot `ping`, foreground `watch`, or detached
  `bg start` with `bg status`, `bg logs -f`, and `bg stop`.
- Shows 5h and weekly usage, reset countdowns, and background watcher state from
  read-only usage endpoints.
- Triggers Claude Code, Codex, and Spark through their official CLIs using your
  existing logged-in credentials.
- Detects active Claude/Codex turns via CLI hooks; Spark uses the Codex hook
  signal because it runs through the Codex CLI.
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
| **Codex** | `…/backend-api/wham/usage` | interactive Codex CLI | OAuth (`~/.codex/auth.json`) |
| **Spark** | `…/backend-api/wham/usage` (`additional_rate_limits`) | interactive Codex CLI with `gpt-5.3-codex-spark` | OAuth (`~/.codex/auth.json`) |

## How it works

Two cleanly separated jobs:

| Job | Mechanism | Cost |
|-----|-----------|------|
| **Trigger** a new window | the official interactive CLI (Claude Code / Codex) | a tiny slice of quota (this is the point) |
| **Read** usage & reset times | zero-quota usage endpoints (the same ones CodexBar / community plugins use) | none — never starts a window |

When `watch` sees a 5h window has reset, it first checks whether a Claude/Codex
session is actively mid-turn. If one is, `limitping` waits and re-reads usage
instead of sending its own ping, because that session's next model request will
start the new window naturally. Spark uses the Codex activity signal. This check
relies on the [CLI hooks](#active-session-detection-hooks) (installed
automatically by the install script); without them, `limitping` skips the check
and pings as soon as the window resets.

- **Claude**: reads `GET https://api.anthropic.com/api/oauth/usage` using the
  OAuth token from the macOS Keychain (`Claude Code-credentials`) or
  `~/.claude/.credentials.json`. Triggering uses a TTY-backed interactive
  `claude "<prompt>"` session, so it continues to start the Claude
  subscription-backed window after the headless print command moves to Agent
  SDK/API credits.
- **Codex**: reads `GET https://chatgpt.com/backend-api/wham/usage` using the
  OAuth token from `~/.codex/auth.json`. Triggering uses a TTY-backed
  interactive `codex "<prompt>"` session; headless `codex exec` can consume
  tokens without anchoring the subscription-backed Codex window.
- **Spark**: uses the same Codex usage endpoint, OAuth token, hooks, and
  interactive CLI path. It reads the `GPT-5.3-Codex-Spark` entry from
  `additional_rate_limits`, sends the ping with model `gpt-5.3-codex-spark`,
  and appears as a separate `spark` provider.

Claude/Codex tokens are reused from the official tools (no separate login) and
refreshed on 401. Spark reuses the Codex token.

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
logged in. Spark uses the Codex CLI credentials.

## Usage

```sh
limitping config init          # write ~/.config/limitping/config.toml
limitping status               # show 5h/weekly % + reset countdowns (alias: s)
limitping status --json        # machine-readable JSON for each provider
limitping status -v            # also print raw JSON
limitping ping                 # trigger all enabled providers now (alias: p)
limitping ping claude          # Claude only
limitping ping codex           # Codex only
limitping ping spark           # Spark only
limitping ping --dry-run       # show the commands without sending
limitping watch                # foreground daemon: ping each window at reset (alias: w)
limitping watch claude         # watch only one provider (claude|codex|spark)
limitping watch --live         # optional live heartbeat/status line
limitping watch --dry-run      # log when pings would fire, without sending
limitping bg start             # run watch in the background, freeing the terminal
limitping bg status            # running? + each watched provider's usage (alias: limitping bg)
limitping bg logs -f           # follow the background watcher's log
limitping bg stop              # stop the background watcher
limitping hooks install        # install active-session detection hooks (claude|codex|all)
limitping hooks uninstall      # remove those hooks
limitping version              # print the version (aliases: v, ver)
limitping upgrade              # update to the latest GitHub release (aliases: up, update)
limitping uninstall            # remove limitping plus config/cache (aliases: rm, remove)
```

Short aliases are also available for config commands: `limitping c i` for
`config init` and `limitping c p` for `config path`.

### Command aliases

`limitping --help` lists aliases inline, for example `ping, p`.

| Command | Aliases |
| --- | --- |
| `status` | `s`, `stat` |
| `ping` | `p` |
| `watch` | `w` |
| `background` | `bg` |
| `config` | `c`, `cfg` |
| `config init` | `c i` |
| `config path` | `c p` |
| `version` | `v`, `ver` |
| `upgrade` | `up`, `update` |
| `uninstall` | `rm`, `remove` |

`ping` shows the exact command and a live timer (a spinner on a terminal).
Current Claude/Codex/Spark interactive trigger sessions do not expose reliable
machine-readable per-ping token or cost data, so success output normally shows
elapsed time only:

```
claude  → claude --model haiku .
claude  ✓ pinged (6.6s)
codex   → codex -c model_reasoning_effort=low -m gpt-5.4-mini ok
codex   ✓ pinged (13.6s)
spark   → codex -c model_reasoning_effort=low -m gpt-5.3-codex-spark ok
spark   ✓ pinged (12.4s)
```

Use `status` or `bg status` for the authoritative 5h/weekly window view after a
ping.

Example `status`:

```
claude
  5h     [█████░░░░░]  51.0%  resets in 3h14m    (Sun 00:10)
  weekly [█████░░░░░]  54.0%  resets in 7h04m    (Sun 04:00)

codex (plus)
  5h     [██░░░░░░░░]  24.0%  resets in 3h15m    (Sun 00:11)
  weekly [████░░░░░░]  37.0%  resets in 111h57m  (Thu 12:53)
```

`status --json` returns the same data as a JSON array (one object per provider),
for scripts and dashboards. Progress chatter is suppressed so stdout stays a
single valid document; a provider that fails to read becomes
`{"provider": "...", "error": "..."}` and the command exits non-zero. Add `-v`
to embed each provider's raw response under `raw`.

```json
[
  {
    "provider": "codex",
    "plan": "plus",
    "five_hour": {
      "used_percent": 24,
      "active": true,
      "resets_at": "2026-06-17T05:51:45+08:00",
      "remaining_seconds": 11700,
      "window_seconds": 18000
    },
    "weekly": {
      "used_percent": 37,
      "active": true,
      "resets_at": "2026-06-24T00:51:45+08:00",
      "remaining_seconds": 403020,
      "window_seconds": 604800
    },
    "credits": { "has_credits": false, "unlimited": false, "balance": "0" },
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

[claude]
enabled    = true
prompt     = "."
model      = "haiku"      # cheapest tier; triggering doesn't need a SOTA model
extra_args = []           # extra Claude CLI args; print/headless-only flags are ignored
align_start = ""          # optional RFC3339 anchor for the first window; empty = start ASAP

[codex]
enabled          = true
prompt           = "ok"
model            = "gpt-5.4-mini"  # cheapest Codex model for triggering
reasoning_effort = "low"  # "minimal" is rejected when web_search/image_gen tools are enabled
extra_args       = []     # extra Codex CLI args; exec-only flags such as --json are ignored
align_start      = ""

[spark]
enabled          = false  # opt in; Spark is a separate Codex-backed watch target
prompt           = "ok"
model            = "gpt-5.3-codex-spark"
reasoning_effort = "low"
extra_args       = []
align_start      = ""
```

Top-level keys:

- **`weekly_threshold`** — when the weekly window is at/above this, `watch` stops
  pinging and waits for the weekly reset (unless usable credits exist).
- **`reset_buffer`** — how long to wait after a window's reset time before
  pinging, so the window has definitely rolled over.
- **`align_start`** (per provider) — pin the phase of your windows: set to a
  future RFC3339 time to delay the very first ping until then; afterwards windows
  chain automatically every ~5h.

### Why a cheap model

Triggering a window doesn't depend on the model — **any** billable request starts
the 5h clock — so the ping uses each provider's cheapest model to eat the least of
your budget:

- **Claude → `haiku`**: also avoids the separate weekly Opus bucket.
- **Codex → `gpt-5.4-mini`**: the mini variant (see `~/.codex/models_cache.json`
  for what your plan offers).
- **Spark → `gpt-5.3-codex-spark`**: a Codex-backed Spark target, disabled by
  default so upgrades do not add another quota-consuming ping.

Claude/Codex/Spark don't expose per-model prices at runtime (Anthropic's local
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
`~/.config/limitping/activity/`. Spark runs through the Codex CLI and uses the
Codex hook/activity marker; there is no separate Spark hook config.

> [!NOTE]
> Claude Code loads its hooks automatically — nothing to do there. **Codex**
> gates custom command hooks behind a one-time trust step: run `/hooks` inside
> Codex once to enable them. Remove everything later with
> `limitping hooks uninstall` (also done automatically by `limitping uninstall`).

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

## Cost & caveats

- See [PRIVACY.md](PRIVACY.md) for local data handling and network behavior.
- See [SECURITY.md](SECURITY.md) for vulnerability reporting and credential
  handling notes.
- Triggering **consumes a little quota** (~one ping per 5h ≈ 33/week). The ping
  uses a minimal prompt and low reasoning, so the cost is tiny but non-zero.
- The **usage endpoints are unofficial** and could change; they're read-only and
  isolated per provider for easy patching.
- macOS-first: Keychain reads and notifications are macOS-only. Codex/Spark
  `auth.json` is cross-platform; Claude on Linux uses
  `~/.claude/.credentials.json`; notifications are a no-op off macOS.

## Layout

```
cmd/limitping            CLI entry
internal/config          TOML config
internal/usage           normalized usage model
internal/auth            Claude (Keychain) + Codex/Spark (auth.json) tokens
internal/provider        per-provider ReadUsage (endpoint) + Trigger (CLI)
internal/activity        hook-based active-session state (shared by the hook cmd + scheduler)
internal/pricing         pricing helpers for providers that expose token usage
internal/scheduler       the watch engine (sleep-until-reset, weekly-respect, backoff)
internal/notify          macOS osascript notifications
internal/cli             cobra commands: status, ping, watch, background, config, hooks, upgrade, uninstall, version
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

**Releasing** is automated: push a tag and GitHub Actions runs GoReleaser to
build the cross-platform binaries and publish a Release.

```sh
git tag v0.2.0 && git push origin v0.2.0
```

## License

[MIT](LICENSE) © wavever
