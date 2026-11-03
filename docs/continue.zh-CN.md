# 自动续跑挂起的任务

[English](continue.md) | **中文**

[文档目录](README.zh-CN.md) · [项目首页](../README.zh-CN.md)

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
