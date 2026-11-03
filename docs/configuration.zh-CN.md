# 配置

[English](configuration.md) | **中文**

[文档目录](README.zh-CN.md) · [项目首页](../README.zh-CN.md)

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
auto_redeem = false       # watch/continue 在重置卡临近过期时自动使用;不可撤销,故默认关闭

[codex]
enabled          = true
prompt           = "ok"
model            = ""     # 留空 = 自动选择你的套餐里最便宜的模型
reasoning_effort = "low"  # 启用 web_search/image_gen 工具时,"minimal" 会被拒绝
extra_args       = []     # 额外 Codex CLI 参数;--json 等 exec-only 参数会被忽略
align_start      = ""
continue_prompt  = "continue"  # continue 在 5h 恢复时注入的消息;留空 = "continue"
auto_redeem = false       # watch/continue 在重置卡临近过期时自动使用;不可撤销,故默认关闭
```

顶层配置项:

- **`weekly_threshold`** —— 周窗口到/超过此值时,`watch` 停止 ping 并等到周重置
  (除非还有可用 credits)。等待期间每 30 分钟重读一次用量,所以期间兑换了重置卡
  也会恢复 ping;用 `limitping redeem` 兑换的话,1 分钟内就会唤醒 watch。
- **`reset_buffer`** —— 在窗口重置时刻之后再等待多久才 ping,确保窗口确实已翻篇。
- **`usage_display`** —— 文本 `status` / `bg status` 使用已用百分比还是剩余百分比。
- **`align_start`**(每个 Provider)—— 固定窗口相位:设为一个未来的 RFC3339 时间,
  把第一次 ping 推迟到那时;之后窗口每 ~5h 自动接龙。
- **`auto_redeem`**(每个 Provider)—— 设为 true 时,`watch` 和 `continue` 会在
  Codex 重置卡或 Claude 重置卡临近过期时自动用掉它。Claude 重置卡是 Anthropic
  不定期发放的一次性额度重置(例如新模型发布时)。`status` 会列出它们;
  `limitping redeem` 手动兑换。攒着的重置卡一旦过期就归零。因为兑换不可撤销,
  默认关闭。

## 为什么用便宜模型

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

活跃会话检测见[活跃会话检测(钩子)](hooks.zh-CN.md)。
