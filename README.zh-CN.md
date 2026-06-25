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

在上一个窗口重置的瞬间,立即启动下一个 **Claude Code** / **Codex** / **Spark** 限额窗口。

Claude Code、Codex 和 Spark 的订阅限额按 **5 小时滚动窗口**(外加周限额)计算。新的 5h 窗口
不会因为上一个窗口重置就自动开始,而是从你下一次真正发起计费请求时才开始。如果你隔了
几个小时才再次使用,这段空档就被浪费了,窗口节奏也会越拖越偏。

`limitping` 会读取每个 Provider 的重置时间,并在窗口翻篇后通过官方 CLI 发一条极小请求。
你可以手动 `ping` 一次,也可以让 `watch` 前台守护,或者用 `bg start` 脱离终端在后台常驻。

```
claude  ✓ pinged (6.6s)
codex   ✓ pinged (13.6s)
spark   ✓ pinged (12.4s)
```

## 亮点

- 在窗口安全重置后立即 ping,让 5 小时窗口连续接上,不被空档拖偏。
- 支持多种运行方式:手动 `ping`、前台 `watch`,或用 `bg start` 后台常驻;配套
  `bg status`、`bg logs -f`、`bg stop` 管理。
- `status` / `bg status` 会展示 5h 与周用量、重置倒计时以及后台监听状态。
- 通过只读用量端点读取状态,通过官方 Claude Code / Codex CLI 触发窗口,复用已有登录态。
- 通过 CLI 钩子识别正在进行中的 Claude/Codex 会话;Spark 通过 Codex CLI 运行,复用 Codex 钩子信号。
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
| **Codex** | `…/backend-api/wham/usage` | 交互式 Codex CLI | OAuth(`~/.codex/auth.json`) |
| **Spark** | `…/backend-api/wham/usage` (`additional_rate_limits`) | 使用 `gpt-5.3-codex-spark` 的交互式 Codex CLI | OAuth(`~/.codex/auth.json`) |

## 工作原理

两件事职责完全分离:

| 任务 | 机制 | 代价 |
|------|------|------|
| **触发**新窗口 | 官方交互式 CLI(Claude Code / Codex) | 消耗一点额度(这正是功能本身) |
| **读取**用量与重置时刻 | 零消耗用量端点(和 CodexBar / 社区插件用的是同一批) | 不消耗,也绝不会起算窗口 |

当 `watch` 发现 5h 窗口已经重置时,会先检查是否有 Claude/Codex 会话正处于对话进行中。
如果有,`limitping` 会等待并重新读取用量,而不是自己发 ping,因为这个会话的下一次模型
请求会自然起算新窗口。Spark 使用 Codex 活跃会话信号。这个检查依赖
[CLI 钩子](#活跃会话检测钩子)(安装脚本会自动装好);未安装钩子时,`limitping` 会跳过该检查,
窗口一重置就直接 ping(绝不靠扫描进程来猜)。

- **Claude**:用 macOS 钥匙串(`Claude Code-credentials`)或 `~/.claude/.credentials.json`
  里的 OAuth token,读 `GET https://api.anthropic.com/api/oauth/usage`。触发使用带
  TTY 的交互式 `claude "<prompt>"` 会话,因此在 headless print 命令改走 Agent
  SDK/API credits 后仍会起算 Claude 订阅窗口。
- **Codex**:用 `~/.codex/auth.json` 里的 OAuth token,读
  `GET https://chatgpt.com/backend-api/wham/usage`。触发使用带 TTY 的交互式
  `codex "<prompt>"` 会话;headless `codex exec` 可能会消耗 token,但不一定起算
  Codex 订阅窗口。
- **Spark**:复用 Codex 用量端点、OAuth token、钩子和交互式 CLI 路径,但从
  `additional_rate_limits` 中读取 `GPT-5.3-Codex-Spark` 条目,用
  `gpt-5.3-codex-spark` 模型发送 ping,并作为独立的 `spark` Provider 展示。

Claude/Codex 的 token 直接复用官方工具(无需另外登录),遇到 401 会自动刷新。Spark 复用
Codex token。

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

你启用的每个 Provider 各自需要凭据:登录好的 `claude` / `codex` CLI。Spark 使用
Codex CLI 凭据。

## 使用

```sh
limitping config init          # 生成 ~/.config/limitping/config.toml
limitping status               # 查看 5h/周 用量百分比 + 重置倒计时(简称: s)
limitping status --json        # 以 JSON 输出每个 Provider 的用量(便于脚本处理)
limitping status -v            # 额外打印原始 JSON
limitping ping                 # 立即触发所有已启用的 Provider(简称: p)
limitping ping claude          # 只触发 Claude
limitping ping codex           # 只触发 Codex
limitping ping spark           # 只触发 Spark
limitping ping --dry-run       # 只打印将执行的命令,不真正发送
limitping watch                # 前台守护:在每个窗口重置时自动 ping(简称: w)
limitping watch claude         # 只监测某一个 Provider(claude|codex|spark)
limitping watch --live         # 可选:显示实时心电图状态行
limitping watch --dry-run      # 只记录何时会触发,不真正发送
limitping bg start             # 在后台运行 watch,释放终端
limitping bg status            # 是否在运行?并列出各 Provider 用量(等同于 limitping bg)
limitping bg logs -f           # 持续查看后台监听的日志
limitping bg stop              # 停止后台监听
limitping hooks install        # 安装活跃会话检测钩子(claude|codex|all)
limitping hooks uninstall      # 移除这些钩子
limitping version              # 打印版本号(简称: v、ver)
limitping upgrade              # 更新到最新 GitHub Release(简称: up; update 是别名)
limitping uninstall            # 删除 limitping 以及配置/缓存(简称: rm、remove)
```

配置命令也支持简称:`limitping c i` 等同于 `config init`, `limitping c p` 等同于
`config path`。

### 命令简称

`limitping --help` 会在命令列表中直接展示简称,例如 `ping, p`。

| 命令 | 简称/别名 |
| --- | --- |
| `status` | `s`、`stat` |
| `ping` | `p` |
| `watch` | `w` |
| `background` | `bg` |
| `config` | `c`、`cfg` |
| `config init` | `c i` |
| `config path` | `c p` |
| `version` | `v`、`ver` |
| `upgrade` | `up`、`update` |
| `uninstall` | `rm`、`remove` |

`ping` 会显示具体命令和实时计时(终端下是 spinner)。当前 Claude/Codex/Spark 都用交互式
触发,CLI 不提供可靠的逐次 machine-readable token/费用数据,所以成功输出通常只显示耗时:

```
claude  → claude --model haiku .
claude  ✓ pinged (6.6s)
codex   → codex -c model_reasoning_effort=low -m gpt-5.4-mini ok
codex   ✓ pinged (13.6s)
spark   → codex -c model_reasoning_effort=low -m gpt-5.3-codex-spark ok
spark   ✓ pinged (12.4s)
```

ping 后请用 `status` 或 `bg status` 查看权威的 5h/周窗口状态。

`status` 示例:

```
claude
  5h     [█████░░░░░]  51.0%  resets in 3h14m    (Sun 00:10)
  weekly [█████░░░░░]  54.0%  resets in 7h04m    (Sun 04:00)

codex (plus)
  5h     [██░░░░░░░░]  24.0%  resets in 3h15m    (Sun 00:11)
  weekly [████░░░░░░]  37.0%  resets in 111h57m  (Thu 12:53)
```

`status --json` 以 JSON 数组返回相同数据(每个 Provider 一个对象),便于脚本和
看板消费。进度提示会被抑制,以保证 stdout 是单个合法 JSON;读取失败的 Provider
会变成 `{"provider": "...", "error": "..."}`,且命令以非零码退出。加上 `-v` 可在
`raw` 字段内嵌入各 Provider 的原始响应。

```json
[
  {
    "provider": "codex",
    "plan": "plus",
    "five_hour": {
      "used_percent": 24,
      "active": true,
      "resets_at": "2026-06-17T05:51:45+08:00",
      "remaining_seconds": 11700,
      "window_seconds": 18000
    },
    "weekly": {
      "used_percent": 37,
      "active": true,
      "resets_at": "2026-06-24T00:51:45+08:00",
      "remaining_seconds": 403020,
      "window_seconds": 604800
    },
    "credits": { "has_credits": false, "unlimited": false, "balance": "0" },
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

[claude]
enabled    = true
prompt     = "."
model      = "haiku"      # 最便宜的档位;触发并不需要 SOTA 模型
extra_args = []           # 额外 Claude CLI 参数;print/headless-only 参数会被忽略
align_start = ""          # 可选 RFC3339:首个窗口的相位锚点;留空 = 尽快开始

[codex]
enabled          = true
prompt           = "ok"
model            = "gpt-5.4-mini"  # 用于触发的最便宜 Codex 模型
reasoning_effort = "low"  # 启用 web_search/image_gen 工具时,"minimal" 会被拒绝
extra_args       = []     # 额外 Codex CLI 参数;--json 等 exec-only 参数会被忽略
align_start      = ""

[spark]
enabled          = false  # 需显式启用;Spark 是独立的 Codex-backed watch 目标
prompt           = "ok"
model            = "gpt-5.3-codex-spark"
reasoning_effort = "low"
extra_args       = []
align_start      = ""
```

顶层配置项:

- **`weekly_threshold`** —— 周窗口到/超过此值时,`watch` 停止 ping 并等到周重置
  (除非还有可用 credits)。
- **`reset_buffer`** —— 在窗口重置时刻之后再等待多久才 ping,确保窗口确实已翻篇。
- **`align_start`**(每个 Provider)—— 固定窗口相位:设为一个未来的 RFC3339 时间,
  把第一次 ping 推迟到那时;之后窗口每 ~5h 自动接龙。

### 为什么用便宜模型

触发窗口和用哪个模型无关——**任何**计费请求都会起算 5h 计时——所以 ping 用每家最便宜
的模型,尽量少吃额度:

- **Claude → `haiku`**:同时避开单独的周 Opus 额度池。
- **Codex → `gpt-5.4-mini`**:mini 变体(你的套餐有哪些见 `~/.codex/models_cache.json`)。
- **Spark → `gpt-5.3-codex-spark`**:一个 Codex-backed Spark 目标;默认关闭,避免升级后
  自动多一次消耗额度的 ping。

Claude/Codex/Spark 运行时都拿不到每个模型的价格(Anthropic 本地价格缓存是空的;Codex
的模型缓存没有价格字段),所以这里用"最便宜模型"作为合理默认,而不是实时查价。需要的话
可按 Provider 覆盖 `model`。

### 活跃会话检测(钩子)

窗口重置时,`watch` 会避免在你正干活时发 ping——你那一轮对话本身就会起算下一个窗口。
这依赖 **CLI 钩子**,安装脚本会自动帮你装好。如果没装钩子,`limitping` 会**跳过**这个检查,
窗口一重置就直接 ping(绝不靠扫描进程来猜)。

安装脚本会自动执行;手动(重新)安装:

```sh
limitping hooks install        # 两个 Provider 都装(或 limitping hooks install claude)
```

这会把 limitping 的钩子写入 `~/.claude/settings.json` 和 `~/.codex/hooks.json`(保留你已有
的配置,并写入 `.bak` 备份)。钩子会在 `UserPromptSubmit` / `PreToolUse` / `PostToolUse` /
`Stop`(Claude 还有 `SessionEnd`)时调用隐藏命令 `limitping hook <provider>`,把会话是否
处于对话进行中记录到 `~/.config/limitping/activity/`。Spark 通过 Codex CLI 运行,复用
Codex 钩子/活跃状态标记,没有单独的 Spark 钩子配置。

> [!NOTE]
> Claude Code 会自动加载钩子,无需操作。**Codex** 对自定义命令钩子要求一次性信任:
> 在 Codex 中运行一次 `/hooks` 启用即可。之后用 `limitping hooks uninstall` 全部移除
> (`limitping uninstall` 也会自动清理)。

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

## 成本与注意事项

- 本地数据处理和网络行为见 [PRIVACY.md](PRIVACY.md)。
- 漏洞报告和凭据处理说明见 [SECURITY.md](SECURITY.md)。
- 触发会**消耗一点额度**(约每 5h 一次 ≈ 每周 33 次)。ping 用最小 prompt + 低 reasoning,
  成本很小但非零。
- **用量端点是非官方接口**,可能变更;它们都是只读的,并按 Provider 隔离,方便单独热修。
- 以 macOS 为主:钥匙串读取和通知仅限 macOS。Codex/Spark 的 `auth.json` 跨平台;Claude
  在 Linux 上用 `~/.claude/.credentials.json`;非 macOS 上通知为空操作。

## 目录结构

```
cmd/limitping            CLI 入口
internal/config          TOML 配置
internal/usage           归一化的用量模型
internal/auth            Claude(钥匙串)+ Codex/Spark(auth.json)token
internal/provider        各 Provider 的 ReadUsage(端点)+ Trigger(CLI)
internal/activity        基于钩子的活跃会话状态(hook 命令与 scheduler 共用)
internal/pricing         为能暴露 token 用量的 Provider 准备的价格辅助代码
internal/scheduler       watch 引擎(sleep 到重置、尊重周限额、退避重试)
internal/notify          macOS osascript 通知
internal/cli             cobra 命令:status、ping、watch、background、config、hooks、upgrade、uninstall、version
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

**发版**是自动的:打一个 tag 并推送,GitHub Actions 会跑 GoReleaser 交叉编译各平台
二进制并发布 Release。

```sh
git tag v0.2.0 && git push origin v0.2.0
```

## 许可证

[MIT](LICENSE) © wavever
