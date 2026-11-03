<p align="center">
  <img src="assets/icon.png" alt="CCLimitPing" width="128">
</p>

<h1 align="center">CCLimitPing</h1>

<p align="center">
  <strong>别再浪费 Claude Code / Codex 的 5 小时窗口。</strong><br>
  <code>limitping</code> 在上一个限额窗口重置的瞬间，自动拉起下一个。
</p>

<p align="center">
  <a href="https://github.com/wavever/CCLimitPing/releases"><img src="https://img.shields.io/github/v/release/wavever/CCLimitPing?include_prereleases&sort=semver" alt="Release"></a>
  <a href="https://github.com/wavever/CCLimitPing/actions/workflows/ci.yml"><img src="https://github.com/wavever/CCLimitPing/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/wavever/CCLimitPing/stargazers"><img src="https://img.shields.io/github/stars/wavever/CCLimitPing?style=flat" alt="Stars"></a>
  <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey" alt="Platform">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-green.svg" alt="MIT"></a>
</p>

<p align="center"><a href="README.md">English</a> · <b>中文</b></p>

## 问题

新的 5 小时窗口，从你发出**第一次计费请求**才开始，不是从上一个窗口重置的那一刻。
晚回来 2 小时，这 2 小时就没了，后面的节奏也会越拖越偏。

```text
没有 limitping   |====== 5h ======|..... 空档 2h .....|====== 5h ======|
有 limitping     |====== 5h ======|====== 5h ======|====== 5h ======|
                                  ↑ 重置瞬间自动 ping
```

## 快速开始

```sh
curl -fsSL https://raw.githubusercontent.com/wavever/CCLimitPing/main/install.sh | sh
limitping status        # 查看 5h / 周用量和重置倒计时
limitping bg start      # 在后台把窗口首尾相接
```

单个二进制，不需要安装 Go。复用你已经登录的 `claude` / `codex`，不新增凭据，没有遥测。
想先看会发生什么、先不花额度？`limitping ping --dry-run`。

## 亮点

- ⏱️ **窗口首尾相接** — 重置后立刻通过官方 CLI ping。
- 🧠 **不打断你正在做的事** — CLI 钩子发现活跃会话时，让这次会话自己起算窗口。
- 🌙 **夜里的任务自动续上** — `limitping continue codex` 在限额恢复的瞬间输入 `continue`（仅 Unix，需要 PTY）。
- 📊 **真实的 token 和费用** — 今日 / 本周 / 本月，读本地会话记录。
- 🎟️ **重置卡别过期作废** — 列出 Codex 重置额度与 Claude 重置卡；可在过期前打开 `auto_redeem`。
- ⌨️ **短命令** — 每条命令都能用 `lmp`（`lmp s`、`lmp w`）。

| Provider | 读取用量（零消耗） | 触发方式 | 鉴权 |
|---|---|---|---|
| **Claude Code** | `…/api/oauth/usage` | 交互式 Claude Code CLI | OAuth（钥匙串 / `~/.claude`） |
| **Codex** | `…/backend-api/wham/usage` | `codex exec --ephemeral` | OAuth（`~/.codex/auth.json`） |

ping 实际跑了什么、用量如何在不起算窗口的情况下读到，见 [工作原理](docs/how-it-works.zh-CN.md)。

## 文档

- [工作原理](docs/how-it-works.zh-CN.md) — ping 发了什么，用量怎么读
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
