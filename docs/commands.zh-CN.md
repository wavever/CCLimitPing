# 命令

[English](commands.md) | **中文**

[文档目录](README.zh-CN.md) · [项目首页](../README.zh-CN.md)

分主题说明：[状态与费用](status.zh-CN.md)、[定时 ping](schedule.zh-CN.md)、[后台 watch](background.zh-CN.md)、[自动续跑](continue.zh-CN.md)、[钩子](hooks.zh-CN.md)、[安装与升级](install.zh-CN.md)。

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

## 命令简称

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

窗口视图、token 费用和 JSON 见[状态、token 与费用](status.zh-CN.md)。
