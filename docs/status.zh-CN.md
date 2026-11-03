# 状态、token 与费用

[English](status.md) | **中文**

[文档目录](README.zh-CN.md) · [项目首页](../README.zh-CN.md)

`status` 示例:

```
claude
  5h     [█████░░░░░]  51.0% 已用  3h14m 后重置 (周日 00:10 UTC+8)
  周     [█████░░░░░]  54.0% 已用  7h04m 后重置 (周日 04:00 UTC+8)
  今日   55.0M token  ≈ $41.03
  本周   310.4M token  ≈ $198.72
  本月   1.02B token  ≈ $684.15

codex (plus)
  5h     当前未生效
  周     [████░░░░░░]  37.0% 已用  111h57m 后重置 (周四 12:53 UTC+8)
  今日   20.5M token  ≈ $10.06
  本周   96.3M token  ≈ $45.80
  本月   412.7M token  ≈ $187.24
  重置券 1 张可用
    - 可用，发放于 06-17 17:38，有效期至 07-17 17:38 UTC+8 (剩 24d6h)
```

(中文环境下输出为中文;英文环境保持 `used` / `resets in` 等英文措辞。)

文本状态默认显示 **used** 百分比。若想和 Codex 界面里的“剩余用量”保持同一口径,
可以设置 `usage_display = "remaining"`。

## 今日 / 本周 / 本月 token 与预估费用

百分比回答不了的问题——本机从本地零点、本周一、本月 1 日起到底用掉了多少 token、
按官方 API 价折算值多少钱(也就是订阅帮你省下了多少)——由 `今日`、`本周`、`本月`
这三行给出。它们是自然日/周/月,不是 Provider 的 5h/周滚动窗口。用量接口只返回百分比,
所以 token 数来自 CLI 自己写在磁盘上的会话记录(`~/.claude/projects`、
`~/.codex/sessions`,并遵循 `$CLAUDE_CONFIG_DIR` / `$CODEX_HOME`),和 ccusage、
CodexBar 的数据源一致;价格用的是 limitping 早已为 `ping` 缓存的
[LiteLLM](https://github.com/BerriAI/litellm) 价格表。全程本地只读,不上传任何内容,
并且与用量请求并发执行;一次扫描本月的会话记录即可得出三个时段,且只打开时段开始后
写过的文件。

两点需要知道:一是**只统计本机**——在别的机器或网页版里跑的会话不会留下本地记录,
也就不会被计入;二是**费用是估算**——订阅本身并不按 token 计费。如果某个模型太新、
价格表里还没有,它的 token 照常统计,只是不计入金额(JSON 中 `cost_complete: false`)。

费用按单次请求计价,与 API 实际计费方式一致:Claude Code 的 1 小时提示缓存写入按 1 小时
费率计(而非 5 分钟费率);单次请求的提示超过模型长上下文阈值(OpenAI 为 272K,
Anthropic 有该档位的模型为 200K)时,整次请求都按长上下文费率计。流式输出的 Claude
回复只计一次,取最终的输出 token 数。

`status -v` 会把每个时段按 token 类型和模型展开:

```
  今日   55.0M token  ≈ $41.03
         输入 770 · 缓存 读 53.6M / 写 1.1M · 输出 300.6K
         claude-opus-5              54.9M token  ≈ $40.95
         claude-haiku-4-5-20251001  67.4K token  ≈ $0.09
  本周   310.4M token  ≈ $198.72
         ...
```

如果某个 Provider 的 CLI 从没在本机跑过,这几行会整体省略——此时保持沉默才是诚实的,
显示 `0 token` 反而像是在说“这个月没用”。

`status --json` 以 JSON 数组返回相同数据(每个 Provider 一个对象),便于脚本和
看板消费。进度提示会被抑制,以保证 stdout 是单个合法 JSON;读取失败的 Provider
会变成 `{"provider": "...", "error": "..."}`,且命令以非零码退出。加上 `-v` 可在
`raw` 字段内嵌入各 Provider 的原始响应。

若某个 Provider 在本机完全没有会话记录,`today`、`week`、`month` 会被整体省略;
存在时,每项都带 `start`(时段的第一个本地日期;`today` 同时保留原来的 `date`),
`cost_usd` 是按 API 价折算的估算值,而当某个跑过的模型没有公开价格时
`cost_complete` 为 false,此时该金额只是下限。

当 Provider 当前不执行某个窗口限制时,对应的窗口键(`five_hour` / `weekly`)会被
省略——例如 OpenAI 于 2026-07-12 临时取消了 Codex 的 5 小时限制,只保留周限额。
文本模式下这类窗口会显示「当前未生效」(英文环境: `not currently enforced`),
`watch` 也会改为在周窗口重置时 ping,而不是每 5 小时一次。

「限制生效但窗口尚未开始」是另一种状态:文本模式显示「无活跃窗口」(英文环境:
`(no active window)`),JSON 中也不会有 `resets_at`——滚动窗口在被请求锚定之前
没有重置时间。Codex 不会直接报告这个状态,它返回的是一个完整长度、且每次读取都
会往后滑动的窗口(`reset_after_seconds` 等于 `limit_window_seconds`),表示「如果
你现在开一个窗口,它会在何时结束」。limitping 会把这种响应归一化掉,因此只有真正
在跑的窗口才会显示重置时间。

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
