# Layout and releasing

**English** | [中文](development.zh-CN.md)

[Docs index](README.md) · [Project README](../README.md)

## Layout

```
cmd/limitping            CLI entry
internal/config          TOML config
internal/usage           normalized usage model
internal/auth            Claude (Keychain) + Codex (auth.json) tokens
internal/provider        per-provider ReadUsage (endpoint) + Trigger (CLI)
internal/activity        hook-based active-session state (shared by the hook cmd + scheduler)
internal/pricing         pricing helpers for providers that expose token usage
internal/spend           today/week/month tokens/cost, read from the CLIs' local session transcripts
internal/scheduler       the watch engine (sleep-until-reset, weekly-respect, backoff)
internal/notify          macOS osascript notifications
internal/cli             cobra commands: status, ping, watch, schedule, continue, background, config, hooks, upgrade, uninstall, version
```

## Contributing

Issues and PRs are welcome. See [CONTRIBUTING.md](../CONTRIBUTING.md) and
[CODE_OF_CONDUCT.md](../CODE_OF_CONDUCT.md). Before submitting:

```sh
gofmt -l .        # should print nothing
go build ./...
go vet ./...
go test ./...
```

Providers are isolated in `internal/provider` behind a small `Provider`
interface (`ReadUsage` + `Trigger`), so adding a new provider is mostly
self-contained provider code plus wiring in `internal/cli` and `internal/config`.

**Releasing** is one command — push a tag, and GitHub Actions runs GoReleaser to
build the cross-platform binaries and publish a Release:

```sh
git tag v0.10.0 && git push origin v0.10.0
```

Nothing else needs editing. The tag is the only place a version is written down:
the release build stamps it in via `-ldflags`, and any other build derives its
version from the module's build info, so there is no constant to bump and
nothing that can drift from the tag. A local `go build` reports
`dev+<revision>` and never offers to upgrade itself.

Release notes are generated from the commit log, so **commit subjects are the
release notes** — write them as a line a user would want to read. There is no
hand-maintained changelog to keep in sync; published notes live on the
[Releases](https://github.com/wavever/CCLimitPing/releases) page.
