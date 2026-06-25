# Privacy

`limitping` is a local command-line tool. It does not include analytics,
telemetry, crash reporting, or advertising trackers.

## Data Processed Locally

Depending on the providers you enable, `limitping` may read:

- Claude Code OAuth credentials from the macOS Keychain or
  `~/.claude/.credentials.json`
- Codex/Spark OAuth credentials from `~/.codex/auth.json` or
  `$CODEX_HOME/auth.json`
- Your `limitping` configuration from `~/.config/limitping/config.toml`
- Provider usage responses used to calculate reset times

The tool may write:

- `~/.config/limitping/config.toml` when you run `limitping config init`
- `~/.config/limitping/litellm_prices.json`, a cached copy of the LiteLLM pricing
  dataset used for Codex cost estimates
- Rotated Claude/Codex/Spark OAuth tokens back to the same credential stores
  used by the official CLIs, when a refresh is required

## Network Requests

`limitping` makes network requests only to support the command you run:

- Anthropic Claude Code OAuth and usage endpoints
- ChatGPT/Codex/Spark OAuth and usage endpoints
- GitHub releases, when using `install.sh`
- The LiteLLM pricing dataset on GitHub, for Codex/Spark equivalent API-cost
  estimates

The tool does not send provider credentials to unrelated services.

## User-Visible Sensitive Output

`limitping status -v` prints raw provider usage responses. Treat this output as
account metadata and avoid posting it publicly.

`ping --dry-run` and `watch --dry-run` print planned commands without sending
provider requests.

## Data Retention

`limitping` does not maintain a usage history database. Provider usage data is
read for the current command and discarded, except for normal terminal output or
logs you choose to keep.

## User Controls

- Disable a provider in `~/.config/limitping/config.toml`
- Delete `~/.config/limitping/litellm_prices.json` to remove the pricing cache
- Run `watch --dry-run` to verify scheduling behavior without sending pings
