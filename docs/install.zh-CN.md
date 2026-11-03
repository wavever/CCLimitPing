# 安装

[English](install.md) | **中文**

[文档目录](README.zh-CN.md) · [项目首页](../README.zh-CN.md)

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
