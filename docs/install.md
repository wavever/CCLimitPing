# Install

**English** | [中文](install.zh-CN.md)

[Docs index](README.md) · [Project README](../README.md)

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
runs at most every four hours, never blocks for more than two seconds, and is skipped
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
