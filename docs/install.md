# Install

**English** | [中文](install.zh-CN.md)

Back to the [README](../README.md) · [Docs index](README.md).

`limitping` ships as a single self-contained binary — **no Go required**.
The one-line install and `limitping upgrade` support **macOS and Linux**:

```sh
curl -fsSL https://raw.githubusercontent.com/wavever/CCLimitPing/main/install.sh | sh
```

Downloads the right prebuilt binary from the
[latest release](https://github.com/wavever/CCLimitPing/releases/latest) into
`/usr/local/bin` (or `~/.local/bin`). Override with `LIMITPING_INSTALL_DIR`.

Each provider you enable needs its own credentials: the `claude` / `codex` CLIs
logged in.

**Windows** release archives are `.zip` only. `install.sh` does not run there,
`limitping upgrade` will not replace a Windows binary, and `limitping continue`
is Unix-only. See [Manual download](#manual-download).

<details>
<summary>Upgrade</summary>

Replace the installed binary with the latest release:

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

On Windows, `upgrade` does not run; download the latest zip from Releases instead.

</details>

<details>
<summary>Uninstall</summary>

Remove the installed binary plus config/cache:

```sh
limitping uninstall
```

Aliases: `limitping rm`, `limitping remove`.

Use `limitping uninstall --keep-config` to preserve `~/.config/limitping` (or
`$XDG_CONFIG_HOME/limitping`).

</details>

<details>
<summary>Manual download</summary>

<a id="manual-download"></a>

Grab the archive from the
[Releases](https://github.com/wavever/CCLimitPing/releases) page.

macOS and Linux ship as `.tar.gz` (the install script picks the matching one):

```sh
tar -xzf limitping_darwin_arm64.tar.gz
sudo mv limitping /usr/local/bin/
```

Windows ships as `.zip` (`limitping_windows_amd64.zip`, `limitping_windows_arm64.zip`).
That zip is a manual drop-in only: the install script rejects Windows,
`limitping upgrade` tells you to download the zip yourself, and
`limitping continue` reports that it is unsupported there because it needs a
Unix PTY. `status`, `ping`, `watch`, and `bg` can still run from the unzipped
binary. Claude credentials on macOS come from the Keychain; elsewhere they come
from `~/.claude/.credentials.json`. Notifications are macOS-only.

</details>

<details>
<summary>From source</summary>

Developers, needs Go 1.25+:

```sh
go install github.com/wavever/CCLimitPing/cmd/limitping@latest
# or, from a clone:
go build -o bin/limitping ./cmd/limitping
```

</details>
