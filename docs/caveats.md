# Cost and caveats

**English** | [中文](caveats.zh-CN.md)

[Docs index](README.md) · [Project README](../README.md)

- See [PRIVACY.md](../PRIVACY.md) for local data handling and network behavior.
- See [SECURITY.md](../SECURITY.md) for vulnerability reporting and credential
  handling notes.
- Triggering **consumes a little quota** (~one ping per 5h ≈ 33/week). The ping
  uses a minimal prompt and low reasoning, so the cost is tiny but non-zero.
- The **usage endpoints are unofficial** and could change; they're read-only and
  isolated per provider for easy patching.
- macOS-first: Keychain reads and notifications are macOS-only. Codex
  `auth.json` is cross-platform; Claude on Linux uses
  `~/.claude/.credentials.json`; notifications are a no-op off macOS.
