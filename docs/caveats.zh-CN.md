# 成本与注意事项

[English](caveats.md) | **中文**

返回[中文 README](../README.zh-CN.md) · [文档目录](README.zh-CN.md)。

- 本地数据处理和网络行为见 [PRIVACY.md](../PRIVACY.md)。
- 漏洞报告和凭据处理说明见 [SECURITY.md](../SECURITY.md)。
- 触发会**消耗一点额度**(约每 5h 一次 ≈ 每周 33 次)。ping 用最小 prompt + 低 reasoning,
  成本很小但非零。
- **用量端点是非官方接口**,可能变更;它们都是只读的,并按 Provider 隔离,方便单独热修。
- 以 macOS 为主:钥匙串读取和通知仅限 macOS。Codex 的 `auth.json` 跨平台;Claude
  在 Linux 上用 `~/.claude/.credentials.json`;非 macOS 上通知为空操作。
