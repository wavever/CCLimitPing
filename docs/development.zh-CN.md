# 目录结构与发版

[English](development.md) | **中文**

返回[中文 README](../README.zh-CN.md) · [文档目录](README.zh-CN.md)。

## 目录结构

```
cmd/limitping            CLI 入口
internal/config          TOML 配置
internal/usage           归一化的用量模型
internal/auth            Claude(钥匙串)+ Codex(auth.json)token
internal/provider        各 Provider 的 ReadUsage(端点)+ Trigger(CLI)
internal/activity        基于钩子的活跃会话状态(hook 命令与 scheduler 共用)
internal/pricing         为能暴露 token 用量的 Provider 准备的价格辅助代码
internal/spend           今日/本周/本月 token 与费用,读取 CLI 的本地会话记录
internal/scheduler       watch 引擎(sleep 到重置、尊重周限额、退避重试)
internal/notify          macOS osascript 通知
internal/cli             cobra 命令:status、ping、watch、schedule、continue、background、config、hooks、upgrade、uninstall、version
```

## 贡献

欢迎提 Issue 和 PR。请先阅读 [CONTRIBUTING.md](../CONTRIBUTING.md) 和
[CODE_OF_CONDUCT.md](../CODE_OF_CONDUCT.md)。提交前请先跑:

```sh
gofmt -l .        # 应当无输出
go build ./...
go vet ./...
go test ./...
```

Provider 都隔离在 `internal/provider`,只需实现一个很小的 `Provider` 接口(`ReadUsage` +
`Trigger`),所以新增一个 Provider 基本是自包含的 Provider 代码,加上在 `internal/cli`
和 `internal/config` 里接一下线。

**发版**只有一条命令 —— 打 tag 并推送,GitHub Actions 会跑 GoReleaser 交叉编译各平台
二进制并发布 Release:

```sh
git tag v0.10.0 && git push origin v0.10.0
```

不需要改任何其他文件。tag 是版本号唯一被写下来的地方:发布构建通过 `-ldflags` 注入,
其他构建从模块自身的 build info 推导,所以没有常量需要手动 bump,也就不可能和 tag 不一致。
本地 `go build` 出来的版本是 `dev+<revision>`,不会提示自我升级。

Release notes 由 commit 日志生成,所以**提交标题就是 release notes** —— 请按"用户愿意读
的一行字"来写。仓库里不再维护手写 changelog,已发布的说明见
[Releases](https://github.com/wavever/CCLimitPing/releases) 页面。
