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
codex   ✓ pinged (14s, 19,426 tokens (in 19,414 / out 12), $0.0023)
```

## Quick start

```sh
curl -fsSL https://raw.githubusercontent.com/wavever/CCLimitPing/main/install.sh | sh
limitping config init
limitping watch
```

`watch` runs in the foreground until Ctrl-C. Add `--dry-run` to preview without
spending quota, or run `limitping bg start` to detach it. Install options,
upgrades, and every other command are in the [docs](docs/README.md).

## Highlights

- Keeps 5h windows continuous by pinging as soon as a reset is safely available.
- One-shot `ping`, foreground `watch`, or detached `bg start`
  (`bg status`, `bg logs -f`, `bg stop`).
- Shows 5h and weekly usage, reset countdowns, and background watcher state.
- Counts today's, this week's, and this month's tokens and API-rate cost from
  local session transcripts.
- Triggers Claude Code and Codex through their official CLIs, using your
  existing login.
- Skips a ping while a Claude or Codex turn is already in progress
  ([hooks](docs/hooks.md)).
- `continue` resumes a parked task the moment the 5h limit recovers.
- Can spend Codex reset credits and Claude reset cards before they expire
  (`redeem`, or `auto_redeem`). Off by default.
- Every command also works as `lmp` (for example `lmp s`, `lmp w`).
- Dry-run, weekly-limit guards, reset buffers, cheap-model defaults, macOS
  notifications, local config, and no telemetry.

## Supported providers

| Provider | Read usage (zero-quota) | Trigger | Auth |
|---|---|---|---|
| **Claude Code** | `…/api/oauth/usage` | interactive Claude Code CLI | OAuth (Keychain / `~/.claude`) |
| **Codex** | `…/backend-api/wham/usage` | `codex exec --ephemeral` | OAuth (`~/.codex/auth.json`) |

## Docs

- [How it works](docs/how-it-works.md) — what a ping sends, and how usage is read
- [Install, upgrade, and uninstall](docs/install.md)
- [Commands](docs/commands.md) — full command list, aliases, and `lmp`
- [Status, tokens, and cost](docs/status.md)
- [Configuration](docs/configuration.md)
- [Active-session hooks](docs/hooks.md)
- [Scheduled pings](docs/schedule.md)
- [Background watch](docs/background.md)
- [Auto-continue](docs/continue.md)
- [Cost and caveats](docs/caveats.md)
- [Layout and releasing](docs/development.md)

[Privacy](PRIVACY.md) · [Security](SECURITY.md) · [Contributing](CONTRIBUTING.md) · [Code of conduct](CODE_OF_CONDUCT.md)

## License

[MIT](LICENSE) © wavever
