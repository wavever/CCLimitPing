# Security Policy

## Supported Versions

Security fixes are prioritized for the latest tagged release and the `main`
branch. If you are running an older binary, upgrade to the latest release before
reporting behavior that may already be fixed.

## Reporting a Vulnerability

Please do not open a public issue with exploit details, tokens, raw account
responses, or private provider metadata.

Use GitHub private vulnerability reporting for this repository when available.
If private reporting is not enabled, open a minimal public issue asking for a
security contact, without including sensitive details.

Helpful reports include:

- The affected command or provider (`claude`, `codex`, or `spark`)
- The operating system and `limitping version`
- A minimal reproduction that does not include credentials
- Whether the issue affects token handling, config files, usage endpoints, or
  command execution

## Scope

High-priority issues include:

- Leaking OAuth tokens, API keys, or raw credential files
- Writing credentials with overly broad file permissions
- Accidentally sending data to an unexpected host
- Shell command injection or unsafe argument handling
- Incorrect behavior that repeatedly consumes provider quota

Out of scope:

- Expected quota consumption from `ping` or `watch`
- Provider API changes that only require compatibility updates
- Vulnerabilities in upstream provider CLIs unless `limitping` exposes them in a
  new way

## Credential Handling Notes

`limitping` reuses credentials from official provider tools where possible:

- Claude Code OAuth credentials from the macOS Keychain or
  `~/.claude/.credentials.json`
- Codex/Spark OAuth credentials from `~/.codex/auth.json` or
  `$CODEX_HOME/auth.json`

Do not share these files or raw `status -v` output in public reports.
