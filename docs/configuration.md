# Configuration

**English** | [中文](configuration.zh-CN.md)

[Docs index](README.md) · [Project README](../README.md)

`~/.config/limitping/config.toml` (honors `$XDG_CONFIG_HOME`):

```toml
weekly_threshold = 0.99   # skip pinging when weekly usage >= this (0..1), until weekly reset
reset_buffer     = "10s"  # wait this long after a reset before pinging (ensures rollover)
notify           = true   # macOS notifications on ping/skip/failure
usage_display    = "used" # text status: "used" or "remaining"

[claude]
enabled    = true
prompt     = "."
model      = "haiku"      # cheapest tier; triggering doesn't need a SOTA model
extra_args = []           # extra Claude CLI args; print/headless-only flags are ignored
align_start = ""          # optional RFC3339 anchor for the first window; empty = start ASAP
continue_prompt = "continue"  # message `continue` injects on 5h recovery; empty = "continue"
auto_redeem = false       # watch/continue spend a near-expiry reset; irreversible, so off

[codex]
enabled          = true
prompt           = "ok"
model            = ""     # empty = pick the cheapest model your plan offers
reasoning_effort = "low"  # "minimal" is rejected when web_search/image_gen tools are enabled
extra_args       = []     # extra Codex CLI args; exec-only flags such as --json are ignored
align_start      = ""
continue_prompt  = "continue"  # message `continue` injects on 5h recovery; empty = "continue"
auto_redeem = false       # watch/continue spend a near-expiry reset; irreversible, so off
```

Top-level keys:

- **`weekly_threshold`** — when the weekly window is at/above this, `watch` stops
  pinging and waits for the weekly reset (unless usable credits exist). It
  re-reads usage every 30 minutes while waiting, so a reset card redeemed in
  the meantime resumes pinging; one redeemed with `limitping redeem` wakes it
  within a minute.
- **`reset_buffer`** — how long to wait after a window's reset time before
  pinging, so the window has definitely rolled over.
- **`usage_display`** — whether text `status` / `bg status` renders each window
  as used percentage or remaining percentage.
- **`align_start`** (per provider) — pin the phase of your windows: set to a
  future RFC3339 time to delay the very first ping until then; afterwards windows
  chain automatically every ~5h.
- **`auto_redeem`** (per provider) — when `true`, `watch` and `continue` spend a
  banked reset once it is close to expiring: a Codex reset credit or a Claude
  reset card (the one-off usage-limit resets Anthropic hands out, for example at
  a model launch). `status` lists them; `limitping redeem` spends one by hand.
  A banked reset is worth nothing after it expires. Off by default, because
  redeeming is irreversible.

## Why a cheap model

Triggering a window doesn't depend on the model — **any** billable request starts
the 5h clock — so the ping uses each provider's cheapest model to eat the least of
your budget:

- **Claude → `haiku`**: also avoids the separate weekly Opus bucket.
- **Codex → empty, resolved per ping**: limitping reads the Codex CLI's own
  catalog (`~/.codex/models_cache.json`) and picks the cheapest model your plan
  offers — the budget tier OpenAI marks "Fast and affordable", never your
  working model, which is usually a far pricier tier. Because the choice is made
  at ping time it survives OpenAI retiring and adding models. Name a model to
  pin one instead; a pinned model that has since been retired is rejected with
  the current list rather than failing as an opaque server error at rollover.

Claude/Codex don't expose per-model prices at runtime (Anthropic's local
cost cache is empty; Codex's model cache has no price field), so the cheapest
model is a sensible default rather than a live price lookup. Override `model`
per provider if you prefer.

Active-session detection is documented in [Active-session detection (hooks)](hooks.md).
