# Commands

**English** | [中文](commands.zh-CN.md)

[Docs index](README.md) · [Project README](../README.md)

Per-command details: [status and cost](status.md), [schedule](schedule.md), [background watch](background.md), [auto-continue](continue.md), [hooks](hooks.md), [install and upgrade](install.md).

```sh
limitping config init          # write ~/.config/limitping/config.toml
limitping status               # 5h/weekly % + reset countdowns + today/week/month tokens (alias: s)
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
limitping redeem --dry-run     # show which reset credit / reset card would be spent
limitping redeem claude        # spend a Claude reset card now (irreversible; claude|codex;
                               # refuses while the windows are under half used — --force)
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

## Command aliases

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
claude  → claude --model haiku --session-id 6f1c2a9e-4b7d-4e2a-9c31-0d5e8f7a1b23 --settings "{\"disableAllHooks\":true,\"promptSuggestionEnabled\":false}" --strict-mcp-config --tools "" -- .
claude  ✓ pinged (6.6s)
codex   → codex exec --ephemeral --json --skip-git-repo-check --disable hooks --sandbox read-only -c model_reasoning_effort=low -m gpt-5.6-luna ok
codex   ✓ pinged (14s, 19,426 tokens (in 19,414 / out 12), $0.0023)
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

The window view, spend totals, and JSON are documented in [Status, tokens, and cost](status.md).
