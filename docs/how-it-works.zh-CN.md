# 工作原理

[English](how-it-works.md) | **中文**

`limitping` 做两件事，而且两条路径互不共用。读取用量永远不会起算窗口；触发窗口会，而且是故意的，走的是官方 CLI。

返回 [中文 README](../README.zh-CN.md) · [文档目录](README.zh-CN.md)。

| 任务 | 机制 | 代价 |
|------|------|------|
| **触发**新窗口 | 官方 CLI（交互式 Claude Code / headless `codex exec`） | 消耗一点额度（这正是功能本身） |
| **读取**用量与重置时刻 | 零消耗用量端点（和 CodexBar / 社区插件用的是同一批） | 不消耗，也绝不会起算窗口 |

当 `watch` 发现 5h 窗口已经重置时，会先检查是否有 Claude/Codex 会话正处于对话进行中。
如果有，`limitping` 会等待并重新读取用量，而不是自己发 ping，因为这个会话的下一次模型
请求会自然起算新窗口。这个检查依赖 [CLI 钩子](hooks.zh-CN.md#活跃会话检测钩子)（安装脚本会自动装好）；
未安装钩子时，Claude 改用 Claude Code 自己的会话列表（`claude agents --json`）判断，Codex 改用
它自己的会话日志（`~/.codex/sessions` 下最后一轮仍未结束的 rollout）判断（绝不靠扫描进程来猜）。

- **Claude**：用 macOS 钥匙串（`Claude Code-credentials`）或 `~/.claude/.credentials.json`
  （设置了 `CLAUDE_CONFIG_DIR` 时与 Claude Code 一样改用该目录）里的 OAuth token，读
  `GET https://api.anthropic.com/api/oauth/usage`。触发使用带
  TTY 的交互式 `claude "<prompt>"` 会话，因此在 headless print 命令改走 Agent
  SDK/API credits 后仍会起算 Claude 订阅窗口。ping 尽量精简：用独立的 `--session-id`
  运行、结束后删除其会话记录（以前每次 ping 都会在 `claude --resume` 里留下一条 "."
  会话），并关闭你的钩子（`disableAllHooks`）、不启动 MCP（`--strict-mcp-config`）、不带
  工具（`--tools ""`）、不读 CLAUDE.md、不写入输入历史 —— 每个参数都只在已安装的 Claude Code
  支持时才加。ping 默认在启动 `watch` 的目录里运行；若 Claude Code 没有信任这个目录（比如家目录，
  Claude Code 不会保存对它的信任），就借用一个 Claude Code 已信任、且没有项目设置的目录，
  免得被工作区信任确认挡住 —— 并且只有在能删掉会话记录时才借用，不会在那里留下任何对话。
  如果用量端点返回语义不明的
  429，limitping 会调用免费且不创建 Message 的 token-counting 端点，区分真实的
  端点限流与 Claude Code 订阅访问被禁用。
- **Codex**：用 `~/.codex/auth.json` 里的 OAuth token，读
  `GET https://chatgpt.com/backend-api/wham/usage`。触发执行
  `codex exec --ephemeral --json "<prompt>"`。ping 之所以走 headless，就是为了
  `--ephemeral`：交互式 CLI 没有办法不落盘会话，所以以前每次 ping 都会在
  `codex resume` 和 Codex Desktop 的对话列表里留下一条 `ok` 会话。`--json` 让 ping
  可验证 —— 它的 `turn.completed` 事件是本地唯一能证明「确实发出了一次计费请求」的
  依据，报告里的 token 数和费用也来自这里。ping 同时带 `--disable hooks` 和
  `--sandbox read-only`：钩子没理由为一个合成会话触发（它也不该把自己登记成活跃的
  Codex 会话），而且这条路径上没有人审阅模型的动作 —— 交互式会话有你在键盘前，它没有。

Claude/Codex 的 token 直接复用官方工具（无需另外登录），遇到 401 会自动刷新。
