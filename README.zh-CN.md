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

## 快速开始

```sh
curl -fsSL https://raw.githubusercontent.com/wavever/CCLimitPing/main/install.sh | sh
limitping config init
limitping watch
```

`watch` 在前台运行,Ctrl-C 停止。加 `--dry-run` 可先看会做什么、但不消耗额度;
`limitping bg start` 则把它脱离终端。安装方式、升级和其余命令见[文档](docs/README.zh-CN.md)。

## 亮点

- 在窗口安全重置后立即 ping,让 5 小时窗口连续接上。
- 手动 `ping`、前台 `watch`,或 `bg start` 后台常驻
  (`bg status`、`bg logs -f`、`bg stop`)。
- 展示 5h 与周用量、重置倒计时和后台监听状态。
- 统计今日、本周、本月的 token 与按 API 价折算的费用,数据来自本地会话记录。
- 通过官方 CLI 触发窗口,复用已有登录态;用量读取不消耗额度。
- 用 [CLI 钩子](docs/hooks.zh-CN.md)避开正在进行的会话,不打断本来就要自己起算窗口的那一轮。
- `continue` 在 5h 限额恢复时自动续跑挂起的任务。
- 可在 Codex 重置卡和 Claude 重置卡过期前用掉它们(`redeem`,或 `auto_redeem`)。默认关闭。
- 所有命令都能用 `lmp`(例如 `lmp s`、`lmp w`)。
- 内置 dry-run、周限额保护、重置缓冲、低成本模型、macOS 通知、本地配置,且不带遥测。

## 支持的 Provider

| Provider | 读取用量(零消耗) | 触发方式 | 鉴权 |
|---|---|---|---|
| **Claude Code** | `…/api/oauth/usage` | 交互式 Claude Code CLI | OAuth(钥匙串 / `~/.claude`) |
| **Codex** | `…/backend-api/wham/usage` | `codex exec --ephemeral` | OAuth(`~/.codex/auth.json`) |

## 文档

- [工作原理](docs/how-it-works.zh-CN.md) — ping 发了什么,用量怎么读
- [安装、升级与卸载](docs/install.zh-CN.md)
- [命令](docs/commands.zh-CN.md) — 完整命令列表、简称和 `lmp`
- [状态、token 与费用](docs/status.zh-CN.md)
- [配置](docs/configuration.zh-CN.md)
- [活跃会话钩子](docs/hooks.zh-CN.md)
- [定时 ping](docs/schedule.zh-CN.md)
- [后台运行 watch](docs/background.zh-CN.md)
- [自动续跑](docs/continue.zh-CN.md)
- [成本与注意事项](docs/caveats.zh-CN.md)
- [目录结构与发版](docs/development.zh-CN.md)

[隐私](PRIVACY.md) · [安全](SECURITY.md) · [贡献](CONTRIBUTING.md) · [行为准则](CODE_OF_CONDUCT.md)

## 许可证

[MIT](LICENSE) © wavever
