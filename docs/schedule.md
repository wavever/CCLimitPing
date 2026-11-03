# Scheduled pings

**English** | [中文](schedule.zh-CN.md)

Back to the [README](../README.md) · [Docs index](README.md).

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
