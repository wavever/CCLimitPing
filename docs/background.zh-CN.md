# 后台运行 watch

[English](background.md) | **中文**

[文档目录](README.zh-CN.md) · [项目首页](../README.zh-CN.md)

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
