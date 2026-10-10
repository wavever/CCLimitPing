<p align="center">
  <img src="assets/icon.png" alt="CCLimitPing icon" width="160">
</p>

# CCLimitPing (`limitping`)

[English](README.md) | **中文**

[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
[![CI](https://github.com/wavever/CCLimitPing/actions/workflows/ci.yml/badge.svg)](https://github.com/wavever/CCLimitPing/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/wavever/CCLimitPing?include_prereleases&sort=semver)](https://github.com/wavever/CCLimitPing/releases)
![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)

在上一个窗口重置的瞬间,立即启动下一个 **Claude Code** / **Codex** 限额窗口。

Claude Code 和 Codex 的订阅限额按 **5 小时滚动窗口**(外加周限额)计算。新的 5h 窗口不会
因为上一个窗口重置就自动开始,而是从你下一次真正发起计费请求时才开始。如果你隔了
几个小时才再次使用,这段空档就被浪费了,窗口节奏也会越拖越偏。

`limitping` 会读取每个 Provider 的重置时间,并在窗口翻篇后通过官方 CLI 发一条极小请求。
你可以手动 `ping` 一次,也可以让 `watch` 前台守护,或者用 `bg start` 脱离终端在后台常驻。

```
claude  ✓ pinged (6.6s)
codex   ✓ pinged (14s, 19,426 tokens (in 19,414 / out 12), $0.0023)
```

## 亮点

- 在窗口安全重置后立即 ping,让 5 小时窗口连续接上,不被空档拖偏。
- 支持多种运行方式:手动 `ping`、前台 `watch`,或用 `bg start` 后台常驻;配套
  `bg status`、`bg logs -f`、`bg stop` 管理。
- `status` / `bg status` 会展示 5h 与周用量、重置倒计时以及后台监听状态。
- 统计今日、本周、本月的 token 消耗与按 API 价折算的预估费用,数据取自 CLI 自己的本地会话记录——
  这是百分比永远给不出的那个数字。
- 通过只读用量端点读取状态,通过官方 Claude Code / Codex CLI 触发窗口,复用已有登录态。
- 通过 CLI 钩子识别正在进行中的 Claude/Codex 会话,不会打断本来就要自己起算窗口的会话。
- 自动续跑挂起的任务:`limitping continue <provider>` 代理官方 CLI,在 5h 限额恢复的
  瞬间自动输入续跑消息,让整夜的任务不用一直停在限额处等你回来。
- 在重置卡过期前用掉它 —— 包括 Codex 重置卡和 Claude 重置卡(Anthropic 不定期发放的
  一次性额度重置,例如新模型发布时):`status` 会列出它们,`limitping redeem` 手动兑换;
  开启 `auto_redeem = true` 后,`watch` / `continue` 会在卡临近过期时自动使用 ——
  攒着的重置卡一旦过期就归零。因为兑换不可撤销,默认关闭。
- 命令名可以更短:所有命令都能用 `lmp` 触发(例如 `lmp s`、`lmp w`)。
- 内置 dry-run、周限额保护、重置缓冲、低成本模型默认值、macOS 通知、本地配置,且不带遥测。

## 快速开始

```sh
curl -fsSL https://raw.githubusercontent.com/wavever/CCLimitPing/main/install.sh | sh
limitping config init
limitping status
limitping ping --dry-run
limitping watch                # 前台低功耗运行(Ctrl-C 停止)
# ...或在后台运行,释放终端:
limitping bg start
limitping bg status
limitping bg logs -f
```

如果你想先确认会发生什么、但不消耗 Provider 额度,可以先运行
`limitping ping --dry-run`、`limitping watch --dry-run` 或
`limitping bg start --dry-run`。

## 支持的 Provider

| Provider | 读取用量(零消耗) | 触发方式 | 鉴权 |
|---|---|---|---|
| **Claude Code** | `…/api/oauth/usage` | 交互式 Claude Code CLI | OAuth(钥匙串 / `~/.claude`) |
| **Codex** | `…/backend-api/wham/usage` | `codex exec --ephemeral` | OAuth(`~/.codex/auth.json`) |

## 工作原理

两件事职责完全分离:

| 任务 | 机制 | 代价 |
|------|------|------|
| **触发**新窗口 | 官方 CLI(交互式 Claude Code / headless `codex exec`) | 消耗一点额度(这正是功能本身) |
| **读取**用量与重置时刻 | 零消耗用量端点(和 CodexBar / 社区插件用的是同一批) | 不消耗,也绝不会起算窗口 |

当 `watch` 发现 5h 窗口已经重置时,会先检查是否有 Claude/Codex 会话正处于对话进行中。
如果有,`limitping` 会等待并重新读取用量,而不是自己发 ping,因为这个会话的下一次模型
请求会自然起算新窗口。这个检查依赖 [CLI 钩子](#活跃会话检测钩子)(安装脚本会自动装好);
未安装钩子时,Claude 改用 Claude Code 自己的会话列表(`claude agents --json`)判断,Codex 改用
它自己的会话日志(`~/.codex/sessions` 下最后一轮仍未结束的 rollout)判断(绝不靠扫描进程来猜)。

- **Claude**:用 macOS 钥匙串(`Claude Code-credentials`)或 `~/.claude/.credentials.json`
  (设置了 `CLAUDE_CONFIG_DIR` 时与 Claude Code 一样改用该目录)里的 OAuth token,读
  `GET https://api.anthropic.com/api/oauth/usage`。触发使用带
  TTY 的交互式 `claude "<prompt>"` 会话,因此在 headless print 命令改走 Agent
  SDK/API credits 后仍会起算 Claude 订阅窗口。ping 尽量精简:用独立的 `--session-id`
  运行、结束后删除其会话记录(以前每次 ping 都会在 `claude --resume` 里留下一条 "."
  会话),并关闭你的钩子(`disableAllHooks`)、不启动 MCP(`--strict-mcp-config`)、不带
  工具(`--tools ""`)、不读 CLAUDE.md、不写入输入历史 —— 每个参数都只在已安装的 Claude Code
  支持时才加。ping 默认在启动 `watch` 的目录里运行;若 Claude Code 没有信任这个目录(比如家目录,
  Claude Code 不会保存对它的信任),就借用一个 Claude Code 已信任、且没有项目设置的目录,
  免得被工作区信任确认挡住 —— 并且只有在能删掉会话记录时才借用,不会在那里留下任何对话。
  如果用量端点返回语义不明的
  429,limitping 会调用免费且不创建 Message 的 token-counting 端点,区分真实的
  端点限流与 Claude Code 订阅访问被禁用。
- **Codex**:用 `~/.codex/auth.json` 里的 OAuth token,读
  `GET https://chatgpt.com/backend-api/wham/usage`。触发执行
  `codex exec --ephemeral --json "<prompt>"`。ping 之所以走 headless,就是为了
  `--ephemeral`:交互式 CLI 没有办法不落盘会话,所以以前每次 ping 都会在
  `codex resume` 和 Codex Desktop 的对话列表里留下一条 `ok` 会话。`--json` 让 ping
  可验证 —— 它的 `turn.completed` 事件是本地唯一能证明「确实发出了一次计费请求」的
  依据,报告里的 token 数和费用也来自这里。ping 同时带 `--disable hooks` 和
  `--sandbox read-only`:钩子没理由为一个合成会话触发(它也不该把自己登记成活跃的
  Codex 会话),而且这条路径上没有人审阅模型的动作 —— 交互式会话有你在键盘前,它没有。

Claude/Codex 的 token 直接复用官方工具(无需另外登录),遇到 401 会自动刷新。

## 安装

`limitping` 是一个自包含的单文件二进制——**普通用户无需安装 Go**。

**一行脚本**(macOS / Linux):

```sh
curl -fsSL https://raw.githubusercontent.com/wavever/CCLimitPing/main/install.sh | sh
```

会从[最新 Release](https://github.com/wavever/CCLimitPing/releases/latest)下载对应
平台的预编译二进制,装到 `/usr/local/bin`(或 `~/.local/bin`)。可用
`LIMITPING_INSTALL_DIR` 覆盖安装目录。

**升级** —— 用最新 Release 替换已安装的二进制:

```sh
limitping upgrade
```

`upgrade` 会先检查,已是最新就直接告诉你;`--force` 可强制重装。`status`、`ping`、
`bg status` 和 `continue` 也会在自己的输出之前提示新版本,每个版本只提示到你处理为止:

```
✨ 有新版本!  0.9.0 -> 0.10.0
   更新说明: https://github.com/wavever/CCLimitPing/releases/latest

     1. 立即更新 (执行 `limitping upgrade`)
   ❯ 2. 跳过
     3. 跳过此版本

   ↑/↓ 移动 · Enter 确认 · Esc 跳过
```

用方向键移动、回车确认(和 Provider CLI 一致);数字键仍可直接选中。光标初始停在
「跳过」——这个提示打断的是你真正要跑的命令,所以回车、Esc、Ctrl-C 都保持原样、
什么都不做。选 3 会把该版本记进 `~/.config/limitping/version.json`,直到下一个版本
才再提醒。检查每 4 小时最多一次、最长阻塞 2 秒,并且在非交互终端下完全跳过 —— 所以
`--json`、`hook` 回调和后台守护进程都不会被打扰。

简称/别名:`limitping up`、`limitping update`。

**卸载** —— 删除已安装的二进制以及配置/缓存:

```sh
limitping uninstall
```

简称/别名:`limitping rm`、`limitping remove`。

使用 `limitping uninstall --keep-config` 可保留 `~/.config/limitping`(或
`$XDG_CONFIG_HOME/limitping`)。

**手动下载** —— 从 [Releases](https://github.com/wavever/CCLimitPing/releases) 页面
下载对应平台的压缩包(macOS/Linux 是 `.tar.gz`,Windows 是 `.zip`):

```sh
tar -xzf limitping_darwin_arm64.tar.gz
sudo mv limitping /usr/local/bin/
```

**Homebrew**(macOS / Linux)—— `brew install wavever/tap/limitping`
_(配好 Homebrew tap 后可用;见 `.goreleaser.yaml`)。_

**从源码**(开发者,需要 Go 1.25+):

```sh
go install github.com/wavever/CCLimitPing/cmd/limitping@latest
# 或在克隆后:
go build -o bin/limitping ./cmd/limitping
```

你启用的每个 Provider 各自需要凭据:登录好的 `claude` / `codex` CLI。

## 使用

```sh
limitping config init          # 生成 ~/.config/limitping/config.toml
limitping status               # 5h/周 用量 + 重置倒计时 + 今日/本周/本月 token(简称: s)
limitping status --json        # 以 JSON 输出每个 Provider 的用量(便于脚本处理)
limitping status -v            # 额外打印分模型明细和原始 JSON
limitping ping                 # 立即触发所有已启用的 Provider(简称: p)
limitping ping claude          # 只触发 Claude
limitping ping codex           # 只触发 Codex
limitping ping --dry-run       # 只打印将执行的命令,不真正发送
limitping watch                # 前台守护:在每个窗口重置时自动 ping(简称: w)
limitping watch claude         # 只监测某一个 Provider(claude|codex)
limitping watch --live         # 可选:显示实时心电图状态行
limitping watch --dry-run      # 只记录何时会触发,不真正发送
limitping schedule codex --at 05:00 --at 13:00  # 按每日指定时间 ping
limitping schedule --every 5h  # 按固定间隔 ping,不跟随重置时间
limitping redeem --dry-run     # 查看会用掉哪一张重置卡
limitping redeem claude        # 立即使用一张 Claude 重置卡(不可撤销;claude|codex;
                               # 窗口用量不到一半时会拒绝,加 --force 强制)
limitping continue codex       # 代理 CLI;5h 限额恢复时自动续跑任务
limitping continue codex --yolo             # Provider 后面的参数原样透传
limitping continue claude --dangerously-skip-permissions
limitping bg start             # 在后台运行 watch,释放终端
limitping bg status            # 是否在运行?并列出各 Provider 用量(等同于 limitping bg)
limitping bg logs -f           # 持续查看后台监听的日志
limitping bg stop              # 停止后台监听
limitping hooks install        # 安装活跃会话检测钩子(claude|codex|all)
limitping hooks uninstall      # 移除这些钩子
limitping hooks status         # 检查是否已安装、是否最新、(Codex)是否已信任
limitping version              # 打印版本号(简称: v、ver)
limitping upgrade              # 更新到最新 GitHub Release(简称: up; update 是别名)
limitping uninstall            # 删除 limitping 以及配置/缓存(简称: rm、remove)
```

配置命令也支持简称:`limitping c i` 等同于 `config init`, `limitping c p` 等同于
`config path`。

### 命令简称

`limitping --help` 会在命令列表中直接展示简称,例如 `ping, p`;顶部的 `别名:` 一行会
同时列出两个程序名,所以用哪个名字调用都能发现另一个。

程序本身也有短名:安装脚本会在 `limitping` 旁边建一个 `lmp` 软链,所以 `lmp status`、
`lmp w` 和 `limitping status` 完全等价。如果 `lmp` 已存在、或在你的 PATH 上已指向别的
命令,安装脚本会跳过这个软链 —— `/usr/local/bin` 里的软链会遮蔽掉与它同名的任何命令。
(从源码构建的话:`ln -s limitping /usr/local/bin/lmp`。)

| 命令 | 简称/别名 |
| --- | --- |
| `status` | `s`、`stat` |
| `ping` | `p` |
| `watch` | `w` |
| `schedule` | `sched` |
| `redeem` | `r` |
| `background` | `bg` |
| `config` | `c`、`cfg` |
| `config init` | `c i` |
| `config path` | `c p` |
| `version` | `v`、`ver` |
| `upgrade` | `up`、`update` |
| `uninstall` | `rm`、`remove` |

`ping` 会显示具体命令和实时计时(终端下是 spinner)。Codex 的 ping 会报告这一轮的
token 数和等价 API 费用,数据来自 `codex exec --json`;Claude 仍用交互式触发,CLI 不
提供可靠的逐次 machine-readable 用量,所以只显示耗时:

```
claude  → claude --model haiku --session-id 6f1c2a9e-4b7d-4e2a-9c31-0d5e8f7a1b23 --settings "{\"disableAllHooks\":true,\"promptSuggestionEnabled\":false}" --strict-mcp-config --tools "" -- .
claude  ✓ pinged (6.6s)
codex   → codex exec --ephemeral --json --skip-git-repo-check --disable hooks --sandbox read-only -c model_reasoning_effort=low -m gpt-5.6-luna ok
codex   ✓ pinged (14s, 19,426 tokens (in 19,414 / out 12), $0.0023)
```

`ping` 和 `watch` 日志始终会显示模型。极少数情况下 limitping 选不出来(磁盘上没有模型
目录,或目录里认不出低成本档),就交回给 Codex CLI 决定,此时模型会标注在命令旁边,
这样仍然能看清这次 ping 消耗在哪个模型上:

```
codex   → codex exec --ephemeral --json … -c model_reasoning_effort=low ok  (模型: gpt-5.6-sol)
```

ping 结束时会把被 ping 的 Provider 的窗口状态一并打印出来(和 `status` 相同)——
一次 ping 远远不足以让用量百分比变动,所以它自己的输出说明不了窗口有没有起算。读取
用量不消耗额度,也不会起算窗口。`--dry-run` 不会读:什么都没发出去,就没有新状态可报。

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

### 今日 / 本周 / 本月 token 与预估费用

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

## 配置

`~/.config/limitping/config.toml`(支持 `$XDG_CONFIG_HOME`):

```toml
weekly_threshold = 0.99   # 周用量 >= 此值(0..1)就跳过 ping,直到周窗口重置
reset_buffer     = "10s"  # 到达重置时刻后再等这么久才 ping(确保窗口已翻篇)
notify           = true   # 在 ping/跳过/失败 时弹 macOS 通知
usage_display    = "used" # 文本状态显示 "used" 或 "remaining"

[claude]
enabled    = true
prompt     = "."
model      = "haiku"      # 最便宜的档位;触发并不需要 SOTA 模型
extra_args = []           # 额外 Claude CLI 参数;print/headless-only 参数会被忽略
align_start = ""          # 可选 RFC3339:首个窗口的相位锚点;留空 = 尽快开始
continue_prompt = "continue"  # continue 在 5h 恢复时注入的消息;留空 = "continue"

[codex]
enabled          = true
prompt           = "ok"
model            = ""     # 留空 = 自动选择你的套餐里最便宜的模型
reasoning_effort = "low"  # 启用 web_search/image_gen 工具时,"minimal" 会被拒绝
extra_args       = []     # 额外 Codex CLI 参数;--json 等 exec-only 参数会被忽略
align_start      = ""
continue_prompt  = "continue"  # continue 在 5h 恢复时注入的消息;留空 = "continue"
```

顶层配置项:

- **`weekly_threshold`** —— 周窗口到/超过此值时,`watch` 停止 ping 并等到周重置
  (除非还有可用 credits)。
- **`reset_buffer`** —— 在窗口重置时刻之后再等待多久才 ping,确保窗口确实已翻篇。
- **`usage_display`** —— 文本 `status` / `bg status` 使用已用百分比还是剩余百分比。
- **`align_start`**(每个 Provider)—— 固定窗口相位:设为一个未来的 RFC3339 时间,
  把第一次 ping 推迟到那时;之后窗口每 ~5h 自动接龙。

### 为什么用便宜模型

触发窗口和用哪个模型无关——**任何**计费请求都会起算 5h 计时——所以 ping 用每家最便宜
的模型,尽量少吃额度:

- **Claude → `haiku`**:同时避开单独的周 Opus 额度池。
- **Codex → 留空,每次 ping 时解析**:limitping 会读取 Codex CLI 自己的模型目录
  (`~/.codex/models_cache.json`),挑出你套餐里最便宜的那个 —— 即 OpenAI 标注为
  "Fast and affordable" 的低成本档,而**不是**你在 Codex 里设的主力模型(通常贵得多)。
  因为是在 ping 时才决定,OpenAI 上下线模型都不影响。想钉死某个模型就直接写上;
  钉死的模型一旦下线会被直接拒绝并列出当前可用列表,而不是等到窗口重置时收到一个
  看不懂的服务端报错。

Claude/Codex 运行时都拿不到每个模型的价格(Anthropic 本地价格缓存是空的;Codex 的模型
缓存没有价格字段),所以这里用"最便宜模型"作为合理默认,而不是实时查价。需要的话可按
Provider 覆盖 `model`。

### 活跃会话检测(钩子)

窗口重置时,`watch` 会避免在你正干活时发 ping——你那一轮对话本身就会起算下一个窗口。
这依赖 **CLI 钩子**,安装脚本会自动帮你装好。如果没装钩子,Claude 会改用 Claude Code 自己的
会话列表(`claude agents --json`,对话进行中的会话标为 `busy`);Codex 则改用它自己的会话日志:
`~/.codex/sessions` 下 10 分钟内写过、且最后一个 `task_started` 之后还没有 `task_complete` 或
`turn_aborted` 的 rollout。`limitping` 绝不靠扫描进程来猜。

安装脚本会自动执行;手动(重新)安装:

```sh
limitping hooks install        # 两个 Provider 都装(或 limitping hooks install claude)
```

这会把 limitping 的钩子写入 `~/.claude/settings.json` 和 `~/.codex/hooks.json`(设置了
`CLAUDE_CONFIG_DIR` / `CODEX_HOME` 时写到对应目录;保留你已有的配置,并写入 `.bak` 备份)。
钩子会在 `UserPromptSubmit` / `PreToolUse` / `PostToolUse` / `Stop` / `SessionEnd`,以及
不经过 `Stop` 就结束一轮对话的事件 —— Claude 的 `StopFailure`(API 错误如触顶结束了这一轮)
和 Codex 的 `Interrupt` —— 时调用隐藏命令 `limitping hook <provider>`,把会话是否处于
对话进行中记录到 `~/.config/limitping/activity/`。`limitping upgrade` 会重新注册这些钩子,
新版本依赖的新事件会自动补上。

```sh
limitping hooks status         # 是否已安装?是否最新?(Codex)是否已信任?
```

`status` 也会提示被其他工具改写掉、或早于当前版本的钩子。

> [!NOTE]
> Claude Code 会自动加载钩子,无需操作。**Codex** 对自定义命令钩子要求一次性信任:
> 在 Codex 中运行一次 `/hooks` 启用即可(新版本新增的钩子也需要再信任一次;
> `limitping hooks status` 会列出 Codex 尚未信任的钩子)。之后用 `limitping hooks uninstall`
> 全部移除(`limitping uninstall` 也会自动清理)。

## 定时 ping

需要按**墙钟时间**执行时,用 `schedule`;需要跟随限额窗口重置接龙时,继续用
`watch`。`schedule` 会以前台常驻方式运行,在下一个固定间隔或每日指定时间点触发
`ping`:

```sh
limitping schedule codex --at 05:00
limitping schedule codex --at 05:00 --at 13:00 --at 21:00
limitping schedule --at 05:00,13:00,21:00
limitping schedule codex --every 5h --dry-run
```

`--every` 和 `--at` 可以一起使用;谁先到就先执行。`--at` 是本地每日时间,格式为
`HH:MM` 或 `HH:MM:SS`。

## 后台运行 `watch`

`watch` 默认前台运行。要释放终端,可用内置的 `bg` 命令把它作为脱离终端的后台进程运行:

```sh
limitping bg start          # 脱离终端,在后台启动 watch
limitping bg status         # 是否在运行?pid、运行时长、日志 + 各 Provider 用量(等同于 limitping bg)
limitping bg logs -f        # 持续查看日志(-n N 查看最后 N 行)
limitping bg stop           # 停止
```

`watch` 默认使用低功耗日志输出;如果需要前台实时心电图状态行,可加 `--live`。
`bg start` 支持与 `watch` 相同的可选 `[provider]` 参数和 `--dry-run` 选项。同一时间只会
运行一个监听(前台或后台),后台输出写入 `~/.config/limitping/bg.log`(遵循 `$XDG_CONFIG_HOME`)。该进程
会脱离到独立会话,关闭终端后依然存活——但**开机不会自启**。

如需在 macOS 上**开机自启**,请改用 `launchd` 服务。创建
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

## 自动续跑挂起的任务

`watch` 和 `bg` 能让窗口链保持接龙,但它们不会去恢复一个已经停在 5h 限额处的任务。
`limitping continue <provider>` 可以:它通过 PTY 启动 Provider 真正的交互式 CLI,把你的
终端原样透传,你照常使用 Codex / Claude Code。它在后台监测用量,当 5h 限额(曾打满)
恢复的瞬间,把续跑消息输入到会话里,让长任务自己接着跑,而不是一直停在限额处等你回来。

```sh
limitping continue codex                       # 照常使用 Codex;恢复时自动续跑
limitping continue codex --yolo                # Provider 后面的参数原样透传给 CLI
limitping continue claude --dangerously-skip-permissions
```

- 续跑消息取配置中各 Provider 的 `continue_prompt`(默认 `"continue"`,可改成如
  `"继续任务"`)。退出请用该 CLI 自带的退出方式。
- 只在真正的恢复边沿注入:5h 窗口曾打满(或端点报告 `limit_reached`,或 CLI 打印过
  限额消息)且随后明确重置,同时周窗口也未耗尽(按 `weekly_threshold`,含 credits)——
  因此不会一恢复就直接撞上周墙。
- 新版 Claude Code 能在限额重置后自己续跑任务(`/config` 中的 "Continue automatically at
  usage limit",默认开启)。`continue claude` 会让它先来:恢复时最多等 90 秒,看 Claude Code
  是否宣布续跑或已开始这一轮,只有没有时才输入续跑消息 —— 任务不会被续两次,旧版
  Claude Code 也照样能续上。
- 诊断时间线写入 `~/.config/limitping/continue.log`(超过 1 MB 轮转为 `continue.log.1`)。
- 目前仅支持 Unix(需要 PTY);在 Windows 上该命令会提示暂不支持。

## 成本与注意事项

- 本地数据处理和网络行为见 [PRIVACY.md](PRIVACY.md)。
- 漏洞报告和凭据处理说明见 [SECURITY.md](SECURITY.md)。
- 触发会**消耗一点额度**(约每 5h 一次 ≈ 每周 33 次)。ping 用最小 prompt + 低 reasoning,
  成本很小但非零。
- **用量端点是非官方接口**,可能变更;它们都是只读的,并按 Provider 隔离,方便单独热修。
- 以 macOS 为主:钥匙串读取和通知仅限 macOS。Codex 的 `auth.json` 跨平台;Claude
  在 Linux 上用 `~/.claude/.credentials.json`;非 macOS 上通知为空操作。

## 目录结构

```
cmd/limitping            CLI 入口
internal/config          TOML 配置
internal/usage           归一化的用量模型
internal/auth            Claude(钥匙串)+ Codex(auth.json)token
internal/provider        各 Provider 的 ReadUsage(端点)+ Trigger(CLI)
internal/activity        基于钩子的活跃会话状态(hook 命令与 scheduler 共用)
internal/pricing         为能暴露 token 用量的 Provider 准备的价格辅助代码
internal/spend           今日/本周/本月 token 与费用,读取 CLI 的本地会话记录
internal/scheduler       watch 引擎(sleep 到重置、尊重周限额、退避重试)
internal/notify          macOS osascript 通知
internal/cli             cobra 命令:status、ping、watch、schedule、continue、background、config、hooks、upgrade、uninstall、version
```

## 贡献

欢迎提 Issue 和 PR。请先阅读 [CONTRIBUTING.md](CONTRIBUTING.md) 和
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。提交前请先跑:

```sh
gofmt -l .        # 应当无输出
go build ./...
go vet ./...
go test ./...
```

Provider 都隔离在 `internal/provider`,只需实现一个很小的 `Provider` 接口(`ReadUsage` +
`Trigger`),所以新增一个 Provider 基本是自包含的 Provider 代码,加上在 `internal/cli`
和 `internal/config` 里接一下线。

**发版**只有一条命令 —— 打 tag 并推送,GitHub Actions 会跑 GoReleaser 交叉编译各平台
二进制并发布 Release:

```sh
git tag v0.10.0 && git push origin v0.10.0
```

不需要改任何其他文件。tag 是版本号唯一被写下来的地方:发布构建通过 `-ldflags` 注入,
其他构建从模块自身的 build info 推导,所以没有常量需要手动 bump,也就不可能和 tag 不一致。
本地 `go build` 出来的版本是 `dev+<revision>`,不会提示自我升级。

Release notes 由 commit 日志生成,所以**提交标题就是 release notes** —— 请按"用户愿意读
的一行字"来写。仓库里不再维护手写 changelog,已发布的说明见
[Releases](https://github.com/wavever/CCLimitPing/releases) 页面。

## 许可证

[MIT](LICENSE) © wavever
