# 活跃会话检测(钩子)

[English](hooks.md) | **中文**

[文档目录](README.zh-CN.md) · [项目首页](../README.zh-CN.md)

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
