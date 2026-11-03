# 定时 ping

[English](schedule.md) | **中文**

[文档目录](README.zh-CN.md) · [项目首页](../README.zh-CN.md)

需要按**墙钟时间**执行时,用 `schedule`;需要跟随限额窗口重置接龙时,继续用
`watch`。`schedule` 会以前台常驻方式运行,在下一个固定间隔或每日指定时间点触发
`ping`:

```sh
limitping schedule codex --at 05:00
limitping schedule codex --at 05:00 --at 13:00 --at 21:00
limitping schedule --at 05:00,13:00,21:00
limitping schedule codex --every 5h --dry-run
```

`--every` 和 `--at` 可以一起使用;谁先到就先执行。`--at` 是本地每日时间,格式为
`HH:MM` 或 `HH:MM:SS`。
