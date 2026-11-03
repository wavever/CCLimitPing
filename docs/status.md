# Status, tokens, and cost

**English** | [中文](status.zh-CN.md)

Back to the [README](../README.md) · [Docs index](README.md).

Example `status`:

```
claude
  5h     [█████░░░░░]  51.0% used      resets in 3h14m    (Sun 00:10 UTC+8)
  weekly [█████░░░░░]  54.0% used      resets in 7h04m    (Sun 04:00 UTC+8)
  today  55.0M tokens  ≈ $41.03
  week   310.4M tokens  ≈ $198.72
  month  1.02B tokens  ≈ $684.15

codex (plus)
  5h     [██░░░░░░░░]  24.0% used      resets in 3h15m    (Sun 00:11 UTC+8)
  weekly [████░░░░░░]  37.0% used      resets in 111h57m  (Thu 12:53 UTC+8)
  today  20.5M tokens  ≈ $10.06
  week   96.3M tokens  ≈ $45.80
  month  412.7M tokens  ≈ $187.24
  reset credits 1 reset available
    - available, granted Jun 17 17:38, expires Jul 17 17:38 UTC+8 (in 24d6h)
```

Text status defaults to **used** percentage. Set `usage_display = "remaining"` if
you prefer the same mental model as Codex's "Usage remaining" UI.

## Tokens and cost: today, this week, this month

The `today`, `week` and `month` lines answer what the percentages cannot: how
much this machine actually consumed since local midnight, since Monday and since
the 1st of the month, and what that would have cost at published API rates —
the value a subscription is returning. These are calendar periods, not the
provider's rolling 5h/weekly windows. The usage endpoints
only ever report percentages, so the tokens are totalled from the transcripts
the CLIs already write to disk (`~/.claude/projects`, `~/.codex/sessions` —
honoring `$CLAUDE_CONFIG_DIR` / `$CODEX_HOME`), the same source ccusage and
CodexBar read, and priced with the [LiteLLM](https://github.com/BerriAI/litellm)
dataset limitping already caches for `ping`. Nothing is uploaded: the scan is
local, read-only, and runs alongside the usage fetch; one pass over the month's
transcripts yields all three periods, and only files written to since the period
began are opened.

Two consequences worth knowing: it is a **local** view — sessions run on another
machine, or in the web app, leave no transcript here and are not counted — and
the cost is an **estimate**, since a subscription does not bill per token. A
model too new to be in the pricing dataset still has its tokens counted; it just
adds nothing to the dollar figure (`cost_complete: false` in JSON).

Each request is priced on its own, the way the API would bill it: Claude Code's
one-hour prompt-cache writes at the one-hour rate rather than the five-minute
one, and a request whose prompt runs past a model's long-context threshold
(272K tokens for OpenAI's, 200K where Anthropic has one) entirely at the
long-context rates. A streamed Claude reply is counted once, at its final output
count.

`status -v` breaks each period down by bucket and by model:

```
  today  55.0M tokens  ≈ $41.03
         in 770 · cache 53.6M read / 1.1M write · out 300.6K
         claude-opus-5              54.9M tokens  ≈ $40.95
         claude-haiku-4-5-20251001  67.4K tokens  ≈ $0.09
  week   310.4M tokens  ≈ $198.72
         ...
```

The lines are omitted entirely for a provider whose CLI has never run on this
machine — silence there is honest, where `0 tokens` would claim a quiet month.

`status --json` returns the same data as a JSON array (one object per provider),
for scripts and dashboards. Progress chatter is suppressed so stdout stays a
single valid document; a provider that fails to read becomes
`{"provider": "...", "error": "..."}` and the command exits non-zero. Add `-v`
to embed each provider's raw response under `raw`.

`today`, `week` and `month` are omitted for a provider with no local transcripts
at all. When present, each carries `start` (the period's first local day; `today`
also keeps it as `date`), `cost_usd` is the API-rate estimate, and
`cost_complete` is false if a model that ran had no published rates, making that
figure a lower bound.

A window key (`five_hour` / `weekly`) is omitted when the provider does not
currently enforce that limit — e.g. OpenAI temporarily removed Codex's 5h
window on 2026-07-12, leaving only the weekly cap. Text mode prints
`not currently enforced` for such a window, and `watch` schedules its ping at
the weekly reset instead of every 5h.

An enforced limit that nothing has started yet is a different state: text mode
prints `(no active window)` and `resets_at` is absent from the JSON, because a
rolling window has no reset time until a request anchors it. Codex does not
report that state directly — it answers with a full-length window that slides
forward on every read (`reset_after_seconds` equal to `limit_window_seconds`),
i.e. when a window *would* end if you started one now. limitping normalizes that
away, so a reset time is only ever shown for a window that is really running.

```json
[
  {
    "provider": "codex",
    "plan": "plus",
    "five_hour": {
      "used_percent": 24,
      "remaining_percent": 76,
      "active": true,
      "resets_at": "2026-06-17T05:51:45+08:00",
      "remaining_seconds": 11700,
      "window_seconds": 18000
    },
    "weekly": {
      "used_percent": 37,
      "remaining_percent": 63,
      "active": true,
      "resets_at": "2026-06-24T00:51:45+08:00",
      "remaining_seconds": 403020,
      "window_seconds": 604800
    },
    "credits": { "has_credits": false, "unlimited": false, "balance": "0" },
    "reset_credits": {
      "available_count": 1,
      "credits": [
        {
          "status": "available",
          "granted_at": "2026-06-17T17:38:38Z",
          "expires_at": "2026-07-17T17:38:38Z"
        }
      ]
    },
    "today": {
      "date": "2026-06-17",
      "start": "2026-06-17",
      "input_tokens": 674291,
      "cache_read_tokens": 19719552,
      "cache_creation_tokens": 0,
      "output_tokens": 81039,
      "total_tokens": 20474882,
      "cost_usd": 10.059257,
      "cost_complete": true,
      "models": [
        { "model": "gpt-5.6-sol", "total_tokens": 13774852, "cost_usd": 7.748941 },
        { "model": "gpt-5.6-terra", "total_tokens": 6700030, "cost_usd": 2.310316 }
      ]
    },
    "week": {
      "start": "2026-06-15",
      "input_tokens": 3102544,
      "cache_read_tokens": 92800114,
      "cache_creation_tokens": 0,
      "output_tokens": 398201,
      "total_tokens": 96300859,
      "cost_usd": 45.801276,
      "cost_complete": true,
      "models": [
        { "model": "gpt-5.6-sol", "total_tokens": 71203817, "cost_usd": 38.412530 },
        { "model": "gpt-5.6-terra", "total_tokens": 25097042, "cost_usd": 7.388746 }
      ]
    },
    "month": {
      "start": "2026-06-01",
      "input_tokens": 13250117,
      "cache_read_tokens": 397840326,
      "cache_creation_tokens": 0,
      "output_tokens": 1609771,
      "total_tokens": 412700214,
      "cost_usd": 187.240913,
      "cost_complete": true,
      "models": [
        { "model": "gpt-5.6-sol", "total_tokens": 298114660, "cost_usd": 154.902207 },
        { "model": "gpt-5.6-terra", "total_tokens": 114585554, "cost_usd": 32.338706 }
      ]
    },
    "limit_reached": false,
    "fetched_at": "2026-06-17T01:00:43+08:00"
  }
]
```
