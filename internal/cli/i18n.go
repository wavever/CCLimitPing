package cli

import (
	"os"
	"strings"
)

type cliText struct {
	rootShort     string
	rootLong      string
	helpFlag      string
	usageTemplate string

	helpCommandShort string
	helpCommandLong  string
	helpUnknownTopic string

	completionShort      string
	completionLong       string
	completionNoDescFlag string
	completionShellShort string
	completionShellLong  string

	versionShort string

	statusShort       string
	statusLong        string
	statusVerboseFlag string
	statusJSONFlag    string
	statusFetchingFmt string

	pingShort      string
	pingLong       string
	pingDryRunFlag string

	watchShort             string
	watchLong              string
	watchDryRunFlag        string
	watchLiveFlag          string
	watchAlreadyRunningFmt string

	bgShort          string
	bgLong           string
	bgExample        string
	bgStartShort     string
	bgStartLong      string
	bgStatusShort    string
	bgStopShort      string
	bgLogsShort      string
	bgLogsFollowFlag string
	bgLogsLinesFlag  string

	// bg runtime (stdout) strings.
	bgHintStart          string
	bgHintManage         string
	bgNotRunning         string
	bgClearedStaleFmt    string
	bgRunningFmt         string
	bgFieldWatching      string
	bgFieldUptime        string
	bgFieldStarted       string
	bgFieldLogs          string
	bgFieldPings         string
	bgPingNone           string
	bgPingSummaryFmt     string
	bgPingShowingLastFmt string
	bgPingSucceeded      string
	bgPingFailed         string
	bgPingDryRun         string
	bgStartedFmt         string
	bgLogPathFmt         string
	bgStartFollowUp      string
	bgStopWasStaleFmt    string
	bgStoppedFmt         string
	bgNoLogYetFmt        string

	configShort     string
	configInitShort string
	configInitForce string
	configPathShort string

	hooksShort          string
	hooksLong           string
	hooksInstallShort   string
	hooksInstallLong    string
	hooksUninstallShort string
	hooksUninstallLong  string
	hooksInstalledFmt   string
	hooksRemovedFmt     string
	hooksNothingFmt     string
	hooksTrustCodex     string

	upgradeShort string
	upgradeLong  string

	uninstallShort      string
	uninstallLong       string
	uninstallKeepConfig string
}

func localizedText() cliText {
	if isChineseLocale() {
		return zhText
	}
	return enText
}

func isChineseLocale() bool {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		locale := strings.ToLower(os.Getenv(key))
		if locale == "" {
			continue
		}
		for _, part := range strings.FieldsFunc(locale, func(r rune) bool {
			return r == ':' || r == '.' || r == '@' || r == '_' || r == '-'
		}) {
			if part == "zh" || strings.HasPrefix(part, "zh") {
				return true
			}
		}
	}
	return false
}

var enText = cliText{
	rootShort: "Keep Claude Code / Codex / Spark rate-limit windows back-to-back",
	rootLong:  "limitping pings your AI coding provider the moment its 5h rate-limit window resets, so the next window starts immediately and stays aligned. Usage is read via zero-quota endpoints; pings go through the official CLIs.",
	helpFlag:  "help for this command",
	usageTemplate: `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .NameAndAliases 24}} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .NameAndAliases 24}} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .NameAndAliases 24}} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`,

	helpCommandShort: "Help about any command",
	helpCommandLong:  "Help provides help for any command in the application.\nType limitping help [command] for full details.",
	helpUnknownTopic: "Unknown help topic",

	completionShort:      "Generate shell completion scripts",
	completionLong:       "Generate shell completion scripts for limitping.\n\nRun `limitping completion [bash|zsh|fish|powershell] --help` for shell-specific usage.",
	completionNoDescFlag: "disable completion descriptions",
	completionShellShort: "Generate the %s completion script",
	completionShellLong:  "Generate the %s completion script for limitping.",

	versionShort: "Print the version",

	statusShort:       "Show current 5h/weekly usage and reset countdowns without using quota",
	statusLong:        "Show current 5h and weekly usage for every enabled provider. This command only reads usage data from zero-quota endpoints; it does not send a ping or consume model quota.",
	statusVerboseFlag: "print the raw JSON response",
	statusJSONFlag:    "output usage as JSON instead of text",
	statusFetchingFmt: "Fetching %s usage...\n",

	pingShort: "Trigger a provider window now with a minimal message",
	pingLong: `Trigger a rate-limit window immediately by sending the minimal message for the selected provider.

Arguments:
  provider  Optional. One of: claude, codex, spark, all.
            Defaults to all, which pings every enabled provider.

Examples:
  limitping ping
  limitping p claude
  limitping ping codex --dry-run`,
	pingDryRunFlag: "print the command without sending",

	watchShort: "Run the foreground daemon and ping each provider when its 5h window resets",
	watchLong: `Run the foreground daemon. When a provider's 5h window resets, limitping sends the minimal message to start the next window.

Arguments:
  provider  Optional. One of: claude, codex, spark, all.
            Defaults to all, which watches every enabled provider.

Examples:
  limitping watch
  limitping w claude
  limitping watch --live
  limitping watch --dry-run`,
	watchDryRunFlag:        "log when pings would fire without sending them",
	watchLiveFlag:          "show a live heartbeat/status line while watching (uses more power)",
	watchAlreadyRunningFmt: "watch already running (pid %d, provider %s%s, started %s); stop it before starting another watcher",

	bgShort: "Run watch in the background — start | stop | status | logs",
	bgLong: `Run the watch daemon detached from the terminal so it keeps pinging across 5h windows after you close the shell.

Subcommands:
  start [provider]   launch the background watcher (also takes --dry-run)
  stop               stop the background watcher
  status             show whether it's running (this is also what bare 'bg' prints)
  logs               show its log output (-f to follow, -n N for the last N lines)

Only one watcher runs at a time, foreground or background. The background process detaches into its own session, so it survives the terminal closing — but it does not restart on reboot (use a launchd/systemd agent for start-at-login).`,
	bgExample:    "  limitping bg start          # start in the background\n  limitping bg start codex    # only Codex\n  limitping bg status         # is it running?  (same as: limitping bg)\n  limitping bg logs -f        # follow the log\n  limitping bg stop           # stop it",
	bgStartShort: "Start watch as a background process",
	bgStartLong: `Launch the watch daemon in the background (detached from the terminal) and return immediately, freeing your shell. Output goes to a log file under the config directory.

Arguments:
  provider  Optional. One of: claude, codex, spark, all.
            Defaults to all, which watches every enabled provider.

Examples:
  limitping bg start
  limitping bg start claude
  limitping bg start --dry-run`,
	bgStatusShort:    "Show whether the background watcher is running",
	bgStopShort:      "Stop the background watcher",
	bgLogsShort:      "Show the background watcher's log output",
	bgLogsFollowFlag: "follow the log output (like tail -f)",
	bgLogsLinesFlag:  "number of trailing log lines to show",

	bgHintStart:          "Start it with: limitping bg start [claude|codex|spark] [--dry-run]",
	bgHintManage:         "Manage it with: limitping bg logs -f  |  limitping bg stop",
	bgNotRunning:         "Background watch: not running.",
	bgClearedStaleFmt:    "Background watch: not running (cleared stale pid %d).\n",
	bgRunningFmt:         "Background watch: running (pid %d).\n",
	bgFieldWatching:      "watching",
	bgFieldUptime:        "uptime",
	bgFieldStarted:       "started",
	bgFieldLogs:          "logs",
	bgFieldPings:         "ping history",
	bgPingNone:           "none recorded since this watcher started",
	bgPingSummaryFmt:     "%d total (%d succeeded, %d failed, %d dry-run)\n",
	bgPingShowingLastFmt: "showing last %d",
	bgPingSucceeded:      "succeeded",
	bgPingFailed:         "failed",
	bgPingDryRun:         "dry-run",
	bgStartedFmt:         "Started background watch (pid %d, provider %s%s).\n",
	bgLogPathFmt:         "Logs: %s\n",
	bgStartFollowUp:      "Check status with `limitping bg status`; stop with `limitping bg stop`.",
	bgStopWasStaleFmt:    "Background watch was not running (cleared stale pid %d).\n",
	bgStoppedFmt:         "Stopped background watch (pid %d).\n",
	bgNoLogYetFmt:        "No log file yet at %s\n",

	configShort:     "Manage the configuration file",
	configInitShort: "Write a default config file",
	configInitForce: "overwrite an existing config",
	configPathShort: "Print the config file path",

	hooksShort: "Manage Claude/Codex hooks for accurate active-session detection",
	hooksLong: `Manage the hooks that let limitping tell whether a Claude Code or Codex session is actually mid-turn (rather than merely running).

When installed, limitping defers its ping while you're actively working and resumes once the turn ends. Spark uses the Codex hook signal because it runs through the Codex CLI. Without hooks limitping skips this check and pings as soon as the window resets. The install script sets these hooks up automatically.`,
	hooksInstallShort: "Register limitping's hooks in the Claude/Codex configs",
	hooksInstallLong: `Register limitping's hooks in ~/.claude/settings.json and ~/.codex/hooks.json (existing settings are preserved; a .bak backup is written).

Arguments:
  provider  Optional. One of: claude, codex, all. Defaults to all.

Claude Code loads its hooks automatically. Codex requires a one-time trust: run /hooks inside Codex to enable them. Spark uses the Codex hook signal.

Examples:
  limitping hooks install
  limitping hooks install claude`,
	hooksUninstallShort: "Remove limitping's hooks from the Claude/Codex configs",
	hooksUninstallLong: `Remove only limitping's hook entries from ~/.claude/settings.json and ~/.codex/hooks.json, leaving your other hooks untouched (a .bak backup is written).

Arguments:
  provider  Optional. One of: claude, codex, all. Defaults to all.

Examples:
  limitping hooks uninstall
  limitping hooks uninstall codex`,
	hooksInstalledFmt: "Installed %s hooks → %s\n",
	hooksRemovedFmt:   "Removed %s hooks from %s\n",
	hooksNothingFmt:   "No %s hooks found in %s\n",
	hooksTrustCodex:   "\nCodex requires a one-time trust: run /hooks inside Codex to enable the new hooks.\n(Claude Code loads its hooks automatically — nothing to do there.)\n",

	upgradeShort: "Upgrade limitping to the latest release",
	upgradeLong:  "Download the latest GitHub release for this OS/architecture and replace the currently running limitping binary.",

	uninstallShort:      "Remove limitping and its config/cache",
	uninstallLong:       "Remove the currently running limitping binary and its config/cache directory. Pass --keep-config to preserve config/cache files.",
	uninstallKeepConfig: "preserve the limitping config/cache directory",
}

var zhText = cliText{
	rootShort: "让 Claude Code / Codex / Spark 的限额窗口自动接龙",
	rootLong:  "limitping 会在 AI 编程 Provider 的 5h 限额窗口重置时立即发送 ping，让下一个窗口马上开始并保持对齐。用量读取走零消耗接口；ping 通过官方 CLI 发送。",
	helpFlag:  "显示此命令的帮助",
	usageTemplate: `用法:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

别名:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

示例:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

可用命令:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .NameAndAliases 24}} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .NameAndAliases 24}} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

其他命令:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .NameAndAliases 24}} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

选项:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

全局选项:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

其他帮助主题:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

使用 "{{.CommandPath}} [command] --help" 查看命令详情。{{end}}
`,

	helpCommandShort: "查看任意命令的帮助",
	helpCommandLong:  "查看应用中任意命令的帮助。\n输入 limitping help [command] 查看完整详情。",
	helpUnknownTopic: "未知帮助主题",

	completionShort:      "生成 shell 补全脚本",
	completionLong:       "生成 limitping 的 shell 补全脚本。\n\n运行 `limitping completion [bash|zsh|fish|powershell] --help` 查看指定 shell 的用法。",
	completionNoDescFlag: "禁用补全说明",
	completionShellShort: "生成 %s 补全脚本",
	completionShellLong:  "生成 limitping 的 %s 补全脚本。",

	versionShort: "打印版本号",

	statusShort:       "查看当前 5h/周用量和重置倒计时，不消耗额度",
	statusLong:        "查看所有已启用 Provider 的当前 5h 和周用量。此命令只通过零消耗接口读取用量，不会发送 ping，也不会消耗模型额度。",
	statusVerboseFlag: "打印原始 JSON 响应",
	statusJSONFlag:    "以 JSON 格式输出用量，而非文本",
	statusFetchingFmt: "正在查询 %s 用量...\n",

	pingShort: "用最小消息立即触发 Provider 的限额窗口",
	pingLong: `通过向指定 Provider 发送最小消息，立即触发一个限额窗口。

参数:
  provider  可选。取值: claude、codex、spark、all。
            默认是 all，会 ping 所有已启用的 Provider。

示例:
  limitping ping
  limitping p claude
  limitping ping codex --dry-run`,
	pingDryRunFlag: "只打印将执行的命令，不真正发送",

	watchShort: "以前台守护方式运行，并在每个 Provider 的 5h 窗口重置时自动 ping",
	watchLong: `以前台守护方式运行。某个 Provider 的 5h 窗口重置后，limitping 会发送最小消息来开启下一个窗口。

参数:
  provider  可选。取值: claude、codex、spark、all。
            默认是 all，会监测所有已启用的 Provider。

示例:
  limitping watch
  limitping w claude
  limitping watch --live
  limitping watch --dry-run`,
	watchDryRunFlag:        "只记录何时会触发，不真正发送",
	watchLiveFlag:          "显示实时心电图状态行（会增加耗电）",
	watchAlreadyRunningFmt: "watch 已在运行（pid %d，Provider %s%s，启动于 %s）；请先停止已有 watcher 再启动新的",

	bgShort: "在后台运行 watch —— start | stop | status | logs",
	bgLong: `以脱离终端的方式在后台运行 watch 守护进程，关闭终端后仍会在每个 5h 窗口重置时持续 ping。

子命令:
  start [provider]   启动后台监听（也支持 --dry-run）
  stop               停止后台监听
  status             查看是否在运行（直接运行 bg 也是这个）
  logs               查看日志（-f 持续跟踪，-n N 查看最后 N 行）

同一时间只会运行一个监听（前台或后台）。后台进程会脱离到独立会话，关闭终端后依然存活——但开机不会自启（如需开机自启，请使用 launchd/systemd 等服务）。`,
	bgExample:    "  limitping bg start          # 在后台启动\n  limitping bg start codex    # 只监测 Codex\n  limitping bg status         # 是否在运行?(等同于 limitping bg)\n  limitping bg logs -f        # 持续查看日志\n  limitping bg stop           # 停止",
	bgStartShort: "以后台进程方式启动 watch",
	bgStartLong: `在后台（脱离终端）启动 watch 守护进程并立即返回，释放当前终端。输出会写入配置目录下的日志文件。

参数:
  provider  可选。取值: claude、codex、spark、all。
            默认是 all，会监测所有已启用的 Provider。

示例:
  limitping bg start
  limitping bg start claude
  limitping bg start --dry-run`,
	bgStatusShort:    "查看后台监听是否在运行",
	bgStopShort:      "停止后台监听",
	bgLogsShort:      "查看后台监听的日志输出",
	bgLogsFollowFlag: "持续跟踪日志输出（类似 tail -f）",
	bgLogsLinesFlag:  "显示最后多少行日志",

	bgHintStart:          "启动: limitping bg start [claude|codex|spark] [--dry-run]",
	bgHintManage:         "管理: limitping bg logs -f  |  limitping bg stop",
	bgNotRunning:         "后台监听：未在运行。",
	bgClearedStaleFmt:    "后台监听：未在运行（已清理失效的 pid %d）。\n",
	bgRunningFmt:         "后台监听：正在运行（pid %d）。\n",
	bgFieldWatching:      "监测",
	bgFieldUptime:        "运行时长",
	bgFieldStarted:       "启动于",
	bgFieldLogs:          "日志",
	bgFieldPings:         "ping 记录",
	bgPingNone:           "本次后台监听启动后暂无记录",
	bgPingSummaryFmt:     "共 %d 次（成功 %d，失败 %d，dry-run %d）\n",
	bgPingShowingLastFmt: "显示最近 %d 条",
	bgPingSucceeded:      "成功",
	bgPingFailed:         "失败",
	bgPingDryRun:         "dry-run",
	bgStartedFmt:         "已在后台启动监听（pid %d，Provider %s%s）。\n",
	bgLogPathFmt:         "日志：%s\n",
	bgStartFollowUp:      "用 `limitping bg status` 查看状态，用 `limitping bg stop` 停止。",
	bgStopWasStaleFmt:    "后台监听原本未在运行（已清理失效的 pid %d）。\n",
	bgStoppedFmt:         "已停止后台监听（pid %d）。\n",
	bgNoLogYetFmt:        "暂无日志文件：%s\n",

	configShort:     "管理配置文件",
	configInitShort: "写入默认配置文件",
	configInitForce: "覆盖已有配置",
	configPathShort: "打印配置文件路径",

	hooksShort: "管理 Claude/Codex 钩子，精确判断会话是否正在运行",
	hooksLong: `管理用于判断 Claude Code 或 Codex 会话是否真正处于对话进行中（而非仅仅进程存在）的钩子。

安装后，limitping 会在你正在使用时推迟 ping，并在一轮对话结束后恢复。Spark 通过 Codex CLI 运行，因此复用 Codex 钩子信号。未安装钩子时，limitping 会跳过该检查，窗口一重置就直接 ping。安装脚本会自动装好这些钩子。`,
	hooksInstallShort: "在 Claude/Codex 配置中注册 limitping 的钩子",
	hooksInstallLong: `在 ~/.claude/settings.json 和 ~/.codex/hooks.json 中注册 limitping 的钩子（保留已有配置，并写入 .bak 备份）。

参数:
  provider  可选。取值: claude、codex、all。默认是 all。

Claude Code 会自动加载钩子；Codex 需要一次性信任：在 Codex 中运行 /hooks 启用它们。Spark 复用 Codex 钩子信号。

示例:
  limitping hooks install
  limitping hooks install claude`,
	hooksUninstallShort: "从 Claude/Codex 配置中移除 limitping 的钩子",
	hooksUninstallLong: `仅从 ~/.claude/settings.json 和 ~/.codex/hooks.json 中移除 limitping 的钩子条目，保留你的其他钩子（会写入 .bak 备份）。

参数:
  provider  可选。取值: claude、codex、all。默认是 all。

示例:
  limitping hooks uninstall
  limitping hooks uninstall codex`,
	hooksInstalledFmt: "已安装 %s 钩子 → %s\n",
	hooksRemovedFmt:   "已从 %s 移除钩子: %s\n",
	hooksNothingFmt:   "%s 中未找到钩子: %s\n",
	hooksTrustCodex:   "\nCodex 需要一次性信任：在 Codex 中运行 /hooks 启用新钩子。\n（Claude Code 会自动加载，无需操作。）\n",

	upgradeShort: "将 limitping 更新到最新版本",
	upgradeLong:  "下载适用于当前系统和架构的最新 GitHub Release，并替换正在运行的 limitping 二进制文件。",

	uninstallShort:      "删除 limitping 及其配置/缓存",
	uninstallLong:       "删除当前运行的 limitping 二进制文件及配置/缓存目录。使用 --keep-config 可保留配置/缓存文件。",
	uninstallKeepConfig: "保留 limitping 配置/缓存目录",
}
