<p align="center">
  <img src="assets/icon.png" alt="CCLimitPing" width="128">
</p>

<h1 align="center">CCLimitPing</h1>

<p align="center">
  <strong>Never waste a minute of your Claude Code / Codex 5-hour window.</strong><br>
  <code>limitping</code> starts your next rate-limit window the instant the last one resets — automatically.
</p>

<p align="center">
  <a href="https://github.com/wavever/CCLimitPing/releases"><img src="https://img.shields.io/github/v/release/wavever/CCLimitPing?include_prereleases&sort=semver" alt="Release"></a>
  <a href="https://github.com/wavever/CCLimitPing/actions/workflows/ci.yml"><img src="https://github.com/wavever/CCLimitPing/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/wavever/CCLimitPing/stargazers"><img src="https://img.shields.io/github/stars/wavever/CCLimitPing?style=flat" alt="Stars"></a>
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey" alt="Platform">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-green.svg" alt="MIT"></a>
</p>

<p align="center"><b>English</b> · <a href="README.zh-CN.md">中文</a></p>

## The problem

A new 5h window only starts when you send your **first billable request** — not when the old one resets.
Come back 2 hours late and those 2 hours are gone, and your schedule drifts.

```text
Without limitping   |====== 5h ======|..... idle 2h .....|====== 5h ======|
With limitping      |====== 5h ======|====== 5h ======|====== 5h ======|
                                     ↑ auto-ping at reset
```

## Quick start

```sh
curl -fsSL https://raw.githubusercontent.com/wavever/CCLimitPing/main/install.sh | sh
limitping status        # see your 5h / weekly usage and reset countdown
limitping bg start      # keep windows back-to-back in the background
```

Single binary, no Go required. Uses your existing `claude` / `codex` login — no new credentials, no telemetry.
Want to look before it spends quota? `limitping ping --dry-run`.

## Highlights

- ⏱️ **Back-to-back windows** — pings each provider right after reset, via the official CLI.
- 🧠 **Never interrupts you** — CLI hooks detect an active session and let it start the window itself.
- 🌙 **Auto-continue overnight tasks** — `limitping continue codex` types "continue" the moment the limit recovers (Unix only; it needs a PTY).
- 📊 **Real token & cost numbers** — today / week / month, from local session transcripts.
- 🎟️ **Don't lose reset credits** — lists Codex reset credits and Claude reset cards; optional `auto_redeem` before expiry.
- ⌨️ **Short to type** — every command works as `lmp` (`lmp s`, `lmp w`).

| Provider | Read usage (zero-quota) | Trigger | Auth |
|---|---|---|---|
| **Claude Code** | `…/api/oauth/usage` | interactive Claude Code CLI | OAuth (Keychain / `~/.claude`) |
| **Codex** | `…/backend-api/wham/usage` | `codex exec --ephemeral` | OAuth (`~/.codex/auth.json`) |

What a ping actually runs, and how usage is read without starting a window: [How it works](docs/how-it-works.md).

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
