# Background watch

**English** | [中文](background.zh-CN.md)

[Docs index](README.md) · [Project README](../README.md)

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
