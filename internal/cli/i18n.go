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
	statusFetchingFmt string // provider; a transient line, erased once read

	// Text-mode usage rendering (status, bg status). The en values must stay
	// byte-identical to the historical hardcoded output.
	statusErrorFmt            string // provider name, error
	statusSubAccessError      string // translation of provider.ClaudeSubscriptionAccessError; empty = print the error's own text
	statusFiveHourLineFmt     string // formatted window
	statusWeeklyLineFmt       string // formatted window
	statusScopedLineFmt       string // scope label (e.g. a model), formatted window
	statusNotEnforced         string
	statusWindowFmt           string // bar, pct, display word, countdown, clock
	statusWindowNoResetFmt    string // bar, pct, display word
	statusUsedWord            string
	statusRemainingWord       string
	statusCreditsUnlimited    string
	statusCreditsFmt          string // balance
	statusResetCreditsOneFmt  string // count (1)
	statusResetCreditsManyFmt string // count (>1)
	// Reset cards the provider withholds from limitping: the line, and the
	// reason in words (shared with redeem).
	statusResetCreditsUnavailableFmt string // cardsUnrecognizedFmt / cardsUnavailableFmt rendered
	statusResetCreditsUnreadFmt      string // error (-v)
	cardsUnrecognizedFmt             string // reason (surface, cli_version)
	cardsUnavailableFmt              string // reason
	statusCreditAvailable            string
	statusCreditRedeemed             string
	statusCreditExpired              string
	statusCreditQueued               string
	statusCreditPaused               string
	statusCreditPending              string
	statusCreditLeftFmt              string // resets left, resets total
	statusCreditClearsFmt            string // window names
	statusCreditAtLimitOnly          string
	statusClearsFiveHour             string
	statusClearsWeekly               string
	statusCreditGrantedFmt           string // datetime
	statusCreditExpiresFmt           string // datetime
	statusCreditExpiresInFmt         string // remaining duration, appended to the expires part
	statusCreditRedeemedFmt          string // datetime
	statusCreditTimeLayout           string
	statusListSep                    string
	statusNowWord                    string
	statusWeekdays                   [7]string // Sunday first; zero value = Go's "Mon" names

	// Today's local token consumption (status, bg status).
	statusTodayLineFmt      string // rendered token/cost summary
	statusWeekSpendLineFmt  string // rendered token/cost summary, since Monday
	statusMonthSpendLineFmt string // rendered token/cost summary, since the 1st
	statusTodayTokensFmt    string // token count
	statusTodayCostFmt      string // cost, appended to the token count
	statusTodayBreakdownFmt string // input, cache read, cache write, output (-v)
	statusTodayModelFmt     string // model, its token/cost summary (-v)
	statusTodayUnknownModel string

	pingShort       string
	pingLong        string
	pingDryRunFlag  string
	pingWouldRunFmt string // provider, command
	pingSendingFmt  string // provider, spinner frame, elapsed
	pingModelFmt    string // model, appended to a command that does not name one
	pingFailedFmt   string // provider, elapsed, error
	pingSuccessFmt  string // provider, elapsed, usage suffix

	watchShort             string
	watchLong              string
	watchDryRunFlag        string
	watchLiveFlag          string
	watchAlreadyRunningFmt string

	scheduleShort      string
	scheduleLong       string
	scheduleEveryFlag  string
	scheduleAtFlag     string
	scheduleStartedFmt string
	scheduleNextFmt    string
	scheduleRunFmt     string
	scheduleErrorFmt   string

	// `continue` interactive proxy strings.
	continueShort       string
	continueLong        string
	continueBadProvider string
	continueStartedFmt  string

	// `redeem` reset-credit strings.
	redeemShort           string
	redeemLong            string
	redeemDryRunFlag      string
	redeemNoneAvailable   string
	redeemPickProviderFmt string // provider list, first provider (for the example)
	redeemForceFlag       string
	redeemLowValueFmt     string // provider, per-window usage ("5h 22%, weekly 4%")
	redeemLowValueNoteFmt string // per-window usage; the dry-run form of redeemLowValueFmt
	redeemPlanFmt         string // provider, label part, expiry part
	redeemPlanLabelFmt    string // card label
	redeemPlanExpiresFmt  string // expiry stamp, remaining lifetime
	redeemDryRunNote      string
	redeemOutcomeFmt      string // provider, outcome sentence
	redeemDone            string
	redeemNothing         string
	redeemNoCredit        string
	redeemAlready         string
	redeemCooldown        string
	redeemIneligible      string
	redeemUnknownFmt      string // raw outcome code
	// Why nothing could be spent, beyond holding no resets.
	redeemCardsUnavailableFmt string // provider, cardsUnrecognizedFmt / cardsUnavailableFmt rendered
	redeemCardsUnreadFmt      string // provider, error
	redeemNoneUsableFmt       string // provider, the held cards' states
	// Outcomes the backend explained.
	redeemCooldownUntilFmt          string // datetime the backend accepts the next reset
	redeemIneligibleUnrecognizedFmt string // reason (surface, cli_version)
	redeemIneligibleReasonFmt       string // reason

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

	hooksStatusShort     string
	hooksStatusLineFmt   string // provider, state word, config path
	hooksStateOff        string
	hooksStateOK         string
	hooksStateOutdated   string
	hooksStateMissing    string
	hooksMissingFmt      string // provider, config path, provider
	hooksOutdatedFmt     string // provider, provider
	hooksTrustUnknownFmt string // error
	hooksUntrustedFmt    string // event=status list
	hooksTrusted         string

	updateAvailableFmt      string // current, next
	updateNotesFmt          string // release notes URL
	updateOptionUpgrade     string // the upgrade command to run
	updateOptionSkip        string
	updateOptionSkipVersion string
	updateChooseHint        string // keys that drive the option menu
	updateDismissedFmt      string // version
	updateFailedFmt         string // error
	updateRerunFmt          string // new version, command line to run again

	upgradeShort      string
	upgradeCurrentFmt string // current version
	upgradeForceFlag  string
	upgradeLong       string

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
	// POSIX precedence: the first set variable decides, so LC_ALL=en_US
	// overrides LANG=zh_CN instead of the zh entry winning from anywhere.
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		locale := strings.ToLower(os.Getenv(key))
		if locale == "" {
			continue
		}
		for _, part := range strings.FieldsFunc(locale, func(r rune) bool {
			return r == ':' || r == '.' || r == '@' || r == '_' || r == '-'
		}) {
			if strings.HasPrefix(part, "zh") {
				return true
			}
		}
		return false
	}
	return false
}

var enText = cliText{
	rootShort: "Keep Claude Code / Codex rate-limit windows back-to-back",
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

	statusShort: "Show current 5h/weekly usage and reset countdowns without using quota",
	statusLong: `Show current 5h and weekly usage for every enabled provider. This command only reads usage data from zero-quota endpoints; it does not send a ping or consume model quota.

The 'today', 'week' and 'month' lines total the tokens this machine's Claude Code / Codex sessions have used since local midnight, since Monday and since the 1st of the month, read from the transcripts those CLIs write to disk, and price them at published API rates — what that use would have cost without the subscription. Work done from another machine or from the web app is not in those logs. Add -v for the per-model breakdown.`,
	statusVerboseFlag: "print the raw JSON response",
	statusJSONFlag:    "output usage as JSON instead of text",
	statusFetchingFmt: "Fetching %s usage...",

	statusErrorFmt:                   "%-7s  error: %v\n",
	statusFiveHourLineFmt:            "  5h     %s\n",
	statusWeeklyLineFmt:              "  weekly %s\n",
	statusScopedLineFmt:              "  %-6s %s\n",
	statusNotEnforced:                "not currently enforced",
	statusWindowFmt:                  "%s %5.1f%% %-9s resets in %-8s (%s)",
	statusWindowNoResetFmt:           "%s %5.1f%% %-9s (no active window)",
	statusUsedWord:                   "used",
	statusRemainingWord:              "remaining",
	statusCreditsUnlimited:           "  credits unlimited\n",
	statusCreditsFmt:                 "  credits %s\n",
	statusResetCreditsOneFmt:         "  reset credits %d reset available\n",
	statusResetCreditsManyFmt:        "  reset credits %d resets available\n",
	statusResetCreditsUnavailableFmt: "  reset credits %s\n",
	statusResetCreditsUnreadFmt:      "  reset credits could not be read: %v\n",
	cardsUnrecognizedFmt:             "not offered: Anthropic did not recognize limitping as the Claude CLI (%s) — check that `claude` is on PATH, since limitping presents itself as the installed Claude Code version",
	cardsUnavailableFmt:              "not offered to this account (%s)",
	statusCreditAvailable:            "available",
	statusCreditRedeemed:             "redeemed",
	statusCreditExpired:              "expired",
	statusCreditQueued:               "queued behind another card",
	statusCreditPaused:               "paused",
	statusCreditPending:              "not usable yet",
	statusCreditLeftFmt:              "%d of %d left",
	statusCreditClearsFmt:            "resets %s",
	statusCreditAtLimitOnly:          "only at the limit",
	statusClearsFiveHour:             "5h",
	statusClearsWeekly:               "weekly",
	statusCreditGrantedFmt:           "granted %s",
	statusCreditExpiresFmt:           "expires %s",
	statusCreditExpiresInFmt:         " (in %s)",
	statusCreditRedeemedFmt:          "redeemed %s",
	statusCreditTimeLayout:           "Jan 02 15:04",
	statusListSep:                    ", ",
	statusNowWord:                    "now",

	statusTodayLineFmt:      "  today  %s\n",
	statusWeekSpendLineFmt:  "  week   %s\n",
	statusMonthSpendLineFmt: "  month  %s\n",
	statusTodayTokensFmt:    "%s tokens",
	statusTodayCostFmt:      "  ≈ $%s",
	statusTodayBreakdownFmt: "         in %s · cache %s read / %s write · out %s\n",
	statusTodayModelFmt:     "         %-26s %s\n",
	statusTodayUnknownModel: "unknown model",

	pingShort: "Trigger a provider window now with a minimal message",
	pingLong: `Trigger a rate-limit window immediately by sending the minimal message for the selected provider.

Arguments:
  provider  Optional. One of: claude, codex, all.
            Defaults to all, which pings every enabled provider.

Examples:
  limitping ping
  limitping p claude
  limitping ping codex --dry-run`,
	pingDryRunFlag:  "print the command without sending",
	pingWouldRunFmt: "%-7s would run: %s\n",
	pingSendingFmt:  "\r%-7s %c sending… %s",
	pingModelFmt:    "  (model: %s)",
	pingFailedFmt:   "%-7s ✗ failed after %s: %v\n",
	pingSuccessFmt:  "%-7s ✓ pinged (%s%s)\n",

	watchShort: "Run the foreground daemon and ping each provider when its 5h window resets",
	watchLong: `Run the foreground daemon. When a provider's 5h window resets, limitping sends the minimal message to start the next window.

Arguments:
  provider  Optional. One of: claude, codex, all.
            Defaults to all, which watches every enabled provider.

Reset credits (Codex reset credits, Claude reset cards): set auto_redeem = true under [claude] or [codex] in the config and watch also spends a banked reset that is about to lapse — within 24h when there is usage worth reclaiming, or in its final hour. Off by default because redeeming is irreversible; 'limitping redeem' spends one by hand.

Examples:
  limitping watch
  limitping w claude
  limitping watch --live
  limitping watch --dry-run`,
	watchDryRunFlag:        "log when pings would fire without sending them",
	watchLiveFlag:          "show a live heartbeat/status line while watching (uses more power)",
	watchAlreadyRunningFmt: "watch already running (pid %d, provider %s%s, started %s); stop it before starting another watcher",

	scheduleShort: "Run scheduled pings at fixed intervals or daily times",
	scheduleLong: `Run scheduled pings for the selected provider. Unlike watch, this follows your wall-clock schedule instead of waiting for the provider's reset time.

Arguments:
  provider  Optional. One of: claude, codex, all.
            Defaults to all, which pings every enabled provider.

Examples:
  limitping schedule codex --at 05:00
  limitping schedule --at 05:00 --at 13:00 --at 21:00
  limitping schedule codex --every 5h --dry-run`,
	scheduleEveryFlag:  "run repeatedly after this interval (for example 5h, 90m)",
	scheduleAtFlag:     "run at a daily local time HH:MM; repeat the flag or use commas for multiple times",
	scheduleStartedFmt: "Scheduled ping for %s (%s%s).\n",
	scheduleNextFmt:    "Next scheduled ping at %s (in %s).\n",
	scheduleRunFmt:     "===== scheduled ping: %s =====\n",
	scheduleErrorFmt:   "schedule run completed with error: %v\n",

	continueShort: "Proxy a provider's CLI and auto-continue its task when the 5h limit recovers",
	continueLong: `Launch a provider's interactive CLI through limitping. Your terminal is passed straight through — you drive Codex / Claude Code exactly as usual — while limitping watches usage in the background and, when the 5h limit recovers after being hit, sends your continue message so a long task resumes itself instead of sitting parked.

Arguments:
  provider     Required. One of: claude, codex.
  cli args...  Optional. Any flags after the provider are forwarded to the CLI
               verbatim, e.g. 'limitping continue codex --yolo'.

The continue message is per-provider continue_prompt in the config (default "continue"; set it to e.g. "继续任务"). Quit from inside the CLI to exit.

Reset credits (Codex reset credits, Claude reset cards): set auto_redeem = true under [claude] or [codex] in the config and the same background watcher also spends a banked reset that is about to lapse — within 24h when there is usage worth reclaiming, or in its final hour — so a parked session can resume without waiting for the window. Off by default because redeeming is irreversible; 'limitping redeem' spends one by hand.

Examples:
  limitping continue codex
  limitping continue codex --yolo
  limitping continue claude --dangerously-skip-permissions`,
	continueBadProvider: "invalid provider (want claude or codex):",
	continueStartedFmt:  "Proxying %s with auto-continue on 5h-limit recovery (message: %q). Use it as usual; quit from inside the CLI to exit.\n",

	redeemShort: "Spend a banked rate-limit reset (Codex reset credit or Claude reset card) now",
	redeemLong: `Consume one of the reset credits shown by 'limitping status' — a Codex reset credit or a Claude reset card — resetting the rate-limit windows it covers.

Arguments:
  provider  Optional. One of: claude, codex.
            Defaults to whichever enabled provider holds a reset; when both
            do, name one.

Redeeming is irreversible. For Codex the credit closest to expiring is spent; for Claude, the card Anthropic has queued next is spent. Either backend refuses — spending nothing — when the reset has nothing to act on: Codex when no window is eligible, Claude when the card only works at a usage limit and you are not at one.

A reset is only worth spending when the windows it restores are well used, so redeem stops — spending nothing — while none of them is at least half used; --force spends it anyway.

When a redemption cannot be confirmed — a timeout, a lost connection, or a backend that has not settled it yet — it may still go through: check 'limitping status' first. Running redeem again is safe either way, because it repeats that same request rather than making a new one, so it can never spend a second reset.

Set auto_redeem = true under [claude] or [codex] in the config to let 'watch' and 'continue' spend a reset on their own once it is close to expiring (within 24h with real usage to reclaim, or in its final hour; a card that only works at a usage limit waits until you are at one).

Examples:
  limitping redeem --dry-run
  limitping redeem claude`,
	redeemDryRunFlag:                "show which credit would be spent without consuming it",
	redeemNoneAvailable:             "no reset credits available to redeem",
	redeemPickProviderFmt:           "reset credits are available for %s; name the one to spend, e.g. 'limitping redeem %s'",
	redeemForceFlag:                 "spend the reset even while the windows it restores are barely used",
	redeemLowValueFmt:               "%s: the windows this reset restores are only %s used, so spending it now would reclaim little; nothing was spent (re-run with --force to spend it anyway)",
	redeemLowValueNoteFmt:           "        note: only %s used — without --force, a real run stops here\n",
	redeemPlanFmt:                   "%-7s redeeming 1 reset credit%s%s\n",
	redeemPlanLabelFmt:              " — %s",
	redeemPlanExpiresFmt:            " (expires %s, in %s)",
	redeemDryRunNote:                "dry run: nothing was consumed\n",
	redeemOutcomeFmt:                "%-7s %s\n",
	redeemDone:                      "redeemed — the eligible rate-limit windows were reset",
	redeemNothing:                   "no rate-limit window is currently eligible for a reset; the credit was not spent",
	redeemNoCredit:                  "the account has no reset credits available",
	redeemAlready:                   "this reset was already used; nothing changed just now",
	redeemCooldown:                  "another reset just went through on this account; nothing was spent — try again in a minute",
	redeemIneligible:                "this reset can't be used any more; nothing was spent",
	redeemUnknownFmt:                "unexpected outcome from the backend: %s",
	redeemCardsUnavailableFmt:       "%s: reset credits %s; nothing was spent",
	redeemCardsUnreadFmt:            "%s: the reset credits could not be read, so nothing was spent (%v)",
	redeemNoneUsableFmt:             "%s: reset credits are held, but none can be used right now (%s); nothing was spent",
	redeemCooldownUntilFmt:          "another reset just went through on this account; nothing was spent — try again after %s",
	redeemIneligibleUnrecognizedFmt: "refused: Anthropic did not recognize limitping as the Claude CLI (%s). The card itself is fine and nothing was spent — check that `claude` is on PATH, since limitping presents itself as the installed Claude Code version",
	redeemIneligibleReasonFmt:       "this reset can't be used right now (%s); nothing was spent",

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
  provider  Optional. One of: claude, codex, all.
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

	bgHintStart:          "Start it with: limitping bg start [claude|codex] [--dry-run]",
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

When installed, limitping defers its ping while you're actively working and resumes once the turn ends. Without them, Claude Code's own session list ('claude agents') answers the same question for Claude, and Codex's own session log (~/.codex/sessions) answers it for Codex. The install script sets these hooks up automatically; 'limitping hooks status' checks they are still installed, current and — for Codex — trusted.`,
	hooksInstallShort: "Register limitping's hooks in the Claude/Codex configs",
	hooksInstallLong: `Register limitping's hooks in ~/.claude/settings.json and ~/.codex/hooks.json (existing settings are preserved; a .bak backup is written).

Arguments:
  provider  Optional. One of: claude, codex, all. Defaults to all.

Claude Code loads its hooks automatically. Codex requires a one-time trust: run /hooks inside Codex to enable them.

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

	hooksStatusShort:     "Check that the Claude/Codex hooks are installed, current and (Codex) trusted",
	hooksStatusLineFmt:   "%-7s %s  (%s)\n",
	hooksStateOff:        "not installed",
	hooksStateOK:         "installed",
	hooksStateOutdated:   "outdated",
	hooksStateMissing:    "missing",
	hooksMissingFmt:      "⚠ %s hooks are gone from %s (another tool may have rewritten it) — run `limitping hooks install %s`\n",
	hooksOutdatedFmt:     "⚠ %s hooks are out of date — run `limitping hooks install %s`\n",
	hooksTrustUnknownFmt: "        could not ask Codex whether it trusts them: %v\n",
	hooksUntrustedFmt:    "⚠ Codex will not run these until you trust them: %s — run /hooks inside Codex\n",
	hooksTrusted:         "        trusted by Codex\n",

	updateAvailableFmt:      "\n\u2728 Update available!  %s -> %s\n",
	updateNotesFmt:          "   Release notes: %s\n\n",
	updateOptionUpgrade:     "Update now (runs `%s`)",
	updateOptionSkip:        "Skip",
	updateOptionSkipVersion: "Skip until next version",
	updateChooseHint:        "\u2191/\u2193 move \u00b7 Enter confirm \u00b7 Esc skip",
	updateDismissedFmt:      "   Skipping %s; you'll hear about the next release.\n",
	updateFailedFmt:         "   %v\n",
	updateRerunFmt:          "   Updated to %s. Run `%s` again to use it.\n",
	upgradeShort:            "Upgrade limitping to the latest release",
	upgradeCurrentFmt:       "limitping %s is already the latest release.\n",
	upgradeForceFlag:        "reinstall even when already on the latest release",
	upgradeLong:             "Download the latest GitHub release for this OS/architecture and replace the currently running limitping binary.",

	uninstallShort:      "Remove limitping and its config/cache",
	uninstallLong:       "Remove the currently running limitping binary and its config/cache directory. Pass --keep-config to preserve config/cache files.",
	uninstallKeepConfig: "preserve the limitping config/cache directory",
}

var zhText = cliText{
	rootShort: "让 Claude Code / Codex 的限额窗口自动接龙",
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

	statusShort: "查看当前 5h/周用量和重置倒计时，不消耗额度",
	statusLong: `查看所有已启用 Provider 的当前 5h 和周用量。此命令只通过零消耗接口读取用量，不会发送 ping，也不会消耗模型额度。

"今日"、"本周"、"本月" 三行分别统计本机 Claude Code / Codex 会话从本地零点、本周一、本月 1 日起消耗的 token，数据来自这些 CLI 写在磁盘上的会话记录，并按官方 API 价格折算——也就是不用订阅时这些用量要花多少钱。其他机器或网页版的用量不在这些记录里。加 -v 可查看分模型明细。`,
	statusVerboseFlag: "打印原始 JSON 响应",
	statusJSONFlag:    "以 JSON 格式输出用量，而非文本",
	statusFetchingFmt: "正在查询 %s 用量...",

	statusErrorFmt:                   "%-7s  错误: %v\n",
	statusSubAccessError:             "Claude 订阅访问不可用（可能是会员已到期/续费失败，或组织管理员禁用了 Claude Code）；请恢复订阅，或在 Claude Code 中改用 Anthropic API Key",
	statusFiveHourLineFmt:            "  5h     %s\n",
	statusWeeklyLineFmt:              "  周     %s\n",
	statusScopedLineFmt:              "  %-6s %s\n",
	statusNotEnforced:                "当前未生效",
	statusWindowFmt:                  "%s %5.1f%% %s  %s 后重置 (%s)",
	statusWindowNoResetFmt:           "%s %5.1f%% %s  (无活跃窗口)",
	statusUsedWord:                   "已用",
	statusRemainingWord:              "剩余",
	statusCreditsUnlimited:           "  credits 不限量\n",
	statusCreditsFmt:                 "  credits %s\n",
	statusResetCreditsOneFmt:         "  重置券 %d 张可用\n",
	statusResetCreditsManyFmt:        "  重置券 %d 张可用\n",
	statusResetCreditsUnavailableFmt: "  重置券 %s\n",
	statusResetCreditsUnreadFmt:      "  重置券 读取失败: %v\n",
	cardsUnrecognizedFmt:             "未向 limitping 提供：Anthropic 没有把 limitping 识别为 Claude CLI（%s）—— 请确认 `claude` 在 PATH 中，limitping 会以本机安装的 Claude Code 版本自报身份",
	cardsUnavailableFmt:              "该账号暂不可用（%s）",
	statusCreditAvailable:            "可用",
	statusCreditRedeemed:             "已兑换",
	statusCreditExpired:              "已过期",
	statusCreditQueued:               "排在另一张之后",
	statusCreditPaused:               "已暂停",
	statusCreditPending:              "暂不可用",
	statusCreditLeftFmt:              "剩 %d/%d 次",
	statusCreditClearsFmt:            "可重置 %s",
	statusCreditAtLimitOnly:          "仅在触顶时可用",
	statusClearsFiveHour:             "5h",
	statusClearsWeekly:               "周限额",
	statusCreditGrantedFmt:           "发放于 %s",
	statusCreditExpiresFmt:           "有效期至 %s",
	statusCreditExpiresInFmt:         " (剩 %s)",
	statusCreditRedeemedFmt:          "兑换于 %s",
	statusCreditTimeLayout:           "01-02 15:04",
	statusListSep:                    "，",
	statusNowWord:                    "现在",
	statusWeekdays:                   [7]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"},

	statusTodayLineFmt:      "  今日   %s\n",
	statusWeekSpendLineFmt:  "  本周   %s\n",
	statusMonthSpendLineFmt: "  本月   %s\n",
	statusTodayTokensFmt:    "%s token",
	statusTodayCostFmt:      "  ≈ $%s",
	statusTodayBreakdownFmt: "         输入 %s · 缓存 读 %s / 写 %s · 输出 %s\n",
	statusTodayModelFmt:     "         %-26s %s\n",
	statusTodayUnknownModel: "未知模型",

	pingShort: "用最小消息立即触发 Provider 的限额窗口",
	pingLong: `通过向指定 Provider 发送最小消息，立即触发一个限额窗口。

参数:
  provider  可选。取值: claude、codex、all。
            默认是 all，会 ping 所有已启用的 Provider。

示例:
  limitping ping
  limitping p claude
  limitping ping codex --dry-run`,
	pingDryRunFlag:  "只打印将执行的命令，不真正发送",
	pingWouldRunFmt: "%-7s 将执行: %s\n",
	pingSendingFmt:  "\r%-7s %c 发送中… %s",
	pingModelFmt:    "  (模型: %s)",
	pingFailedFmt:   "%-7s ✗ 失败 (耗时 %s): %v\n",
	pingSuccessFmt:  "%-7s ✓ 已 ping (%s%s)\n",

	watchShort: "以前台守护方式运行，并在每个 Provider 的 5h 窗口重置时自动 ping",
	watchLong: `以前台守护方式运行。某个 Provider 的 5h 窗口重置后，limitping 会发送最小消息来开启下一个窗口。

参数:
  provider  可选。取值: claude、codex、all。
            默认是 all，会监测所有已启用的 Provider。

重置卡（Codex 重置卡、Claude 重置卡）: 在配置的 [claude] 或 [codex] 下设置 auto_redeem = true，watch 还会在重置卡临近过期时自动用掉它——剩余有效期 24h 内且确实有用量可回收，或进入最后 1 小时。因为兑换不可撤销，默认关闭；手动兑换用 'limitping redeem'。

示例:
  limitping watch
  limitping w claude
  limitping watch --live
  limitping watch --dry-run`,
	watchDryRunFlag:        "只记录何时会触发，不真正发送",
	watchLiveFlag:          "显示实时心电图状态行（会增加耗电）",
	watchAlreadyRunningFmt: "watch 已在运行（pid %d，Provider %s%s，启动于 %s）；请先停止已有 watcher 再启动新的",

	scheduleShort: "按固定间隔或每日指定时间执行 ping",
	scheduleLong: `按用户指定的时间表执行 ping。它和 watch 不同: schedule 跟随你的墙钟时间,不会等待 Provider 的限额重置时刻。

参数:
  provider  可选。取值: claude、codex、all。
            默认是 all，会 ping 所有已启用的 Provider。

示例:
  limitping schedule codex --at 05:00
  limitping schedule --at 05:00 --at 13:00 --at 21:00
  limitping schedule codex --every 5h --dry-run`,
	scheduleEveryFlag:  "按固定间隔重复执行（例如 5h、90m）",
	scheduleAtFlag:     "按每日本地时间 HH:MM 执行；可重复传入，也可用逗号写多个",
	scheduleStartedFmt: "已为 %s 启动定时 ping（%s%s）。\n",
	scheduleNextFmt:    "下次定时 ping: %s（还有 %s）。\n",
	scheduleRunFmt:     "===== 定时 ping: %s =====\n",
	scheduleErrorFmt:   "本次定时执行完成，但有错误: %v\n",

	continueShort: "代理该 Provider 的 CLI，并在 5h 限额恢复时自动续跑任务",
	continueLong: `通过 limitping 启动该 Provider 的交互式 CLI。你的终端会被原样透传——照常使用 Codex / Claude Code——同时 limitping 在后台监测用量，当 5h 限额（曾打满）恢复时自动发送续跑消息，让长任务自己接着跑，而不是停在限额处。

参数:
  provider     必填。取值: claude、codex。
  cli args...  可选。Provider 后面的参数会原样转发给该 CLI，例如
               'limitping continue codex --yolo'。

续跑消息取配置中各 Provider 的 continue_prompt（默认 "continue"，可改成如 "继续任务"）。退出请用该 CLI 自带的退出方式。

重置卡（Codex 重置卡、Claude 重置卡）: 在配置的 [claude] 或 [codex] 下设置 auto_redeem = true，后台的同一个 watcher 还会在重置卡临近过期时自动用掉它——剩余有效期 24h 内且确实有用量可回收，或进入最后 1 小时——这样停在限额处的会话不必干等窗口重置。因为兑换不可撤销，默认关闭；手动兑换用 'limitping redeem'。

示例:
  limitping continue codex
  limitping continue codex --yolo
  limitping continue claude --dangerously-skip-permissions`,
	continueBadProvider: "无效的 Provider（应为 claude 或 codex）：",
	continueStartedFmt:  "正在代理 %s，5h 限额恢复后会自动续跑（消息：%q）。照常使用；退出请用该 CLI 自带的退出方式。\n",

	redeemShort: "立即使用一张已到账的限额重置卡（Codex 或 Claude）",
	redeemLong: `消耗一张 'limitping status' 中显示的重置卡（Codex 重置卡或 Claude 重置卡），重置它能覆盖的限额窗口。

参数:
  provider  可选。取值: claude、codex。
            默认用持有重置卡的那个已启用 Provider；两边都有时需指明。

兑换不可撤销。Codex 用的是最快过期的那张；Claude 用的是 Anthropic 排在最前面的那张。重置无事可做时两边后端都会拒绝且不消耗卡：Codex 是当前没有可重置的窗口，Claude 是这张卡仅在触顶时可用而你尚未触顶。

重置卡只有在它能重置的窗口用得够多时才值得用：这些窗口没有一个用到一半时，redeem 会停下且不消耗卡；加 --force 照样使用。

兑换结果无法确认时（超时、连接中断，或后端尚未结算），它仍可能已经生效：请先看 'limitping status'。无论如何再次运行 redeem 都是安全的 —— 它会重发同一个请求而不是新建一个，因此绝不会多用掉一张。

在配置的 [claude] 或 [codex] 下设置 auto_redeem = true，可让 'watch' 和 'continue' 在卡临近过期时自动使用（剩余有效期 24h 内且确实有用量可回收，或进入最后 1 小时；仅在触顶时可用的卡会等到真正触顶再用）。

示例:
  limitping redeem --dry-run
  limitping redeem claude`,
	redeemDryRunFlag:                "只显示会用掉哪一张，不实际消耗",
	redeemNoneAvailable:             "没有可用的重置卡",
	redeemPickProviderFmt:           "%s 都有可用的重置卡；请指明要用哪个，例如 'limitping redeem %s'",
	redeemForceFlag:                 "即使能重置的窗口几乎没用，也照样使用",
	redeemLowValueFmt:               "%s: 这张卡能重置的窗口目前只用了 %s，现在用基本是浪费；本次未消耗（确实要用请加 --force 重新运行）",
	redeemLowValueNoteFmt:           "        注意: 目前只用了 %s —— 实际运行时不加 --force 会在这里停下\n",
	redeemPlanFmt:                   "%-7s 即将使用 1 张重置卡%s%s\n",
	redeemPlanLabelFmt:              "「%s」",
	redeemPlanExpiresFmt:            "（有效期至 %s，剩 %s）",
	redeemDryRunNote:                "dry run: 未消耗任何重置卡\n",
	redeemOutcomeFmt:                "%-7s %s\n",
	redeemDone:                      "已兑换 —— 符合条件的限额窗口已重置",
	redeemNothing:                   "当前没有可重置的限额窗口，本次未消耗重置卡",
	redeemNoCredit:                  "账号没有可用的重置卡",
	redeemAlready:                   "这张重置卡已经用过，本次没有任何变化",
	redeemCooldown:                  "账号上刚有另一次重置生效，本次未消耗重置卡 —— 请一分钟后再试",
	redeemIneligible:                "这张重置卡已不可用，本次未消耗",
	redeemUnknownFmt:                "后端返回了未知结果: %s",
	redeemCardsUnavailableFmt:       "%s: 重置卡%s；本次未消耗",
	redeemCardsUnreadFmt:            "%s: 无法读取重置卡，本次未消耗（%v）",
	redeemNoneUsableFmt:             "%s: 持有重置卡，但目前没有一张能用（%s）；本次未消耗",
	redeemCooldownUntilFmt:          "账号上刚有另一次重置生效，本次未消耗重置卡 —— 请在 %s 之后再试",
	redeemIneligibleUnrecognizedFmt: "被拒绝：Anthropic 没有把 limitping 识别为 Claude CLI（%s）。卡本身没有问题，本次未消耗 —— 请确认 `claude` 在 PATH 中，limitping 会以本机安装的 Claude Code 版本自报身份",
	redeemIneligibleReasonFmt:       "这张重置卡目前不能使用（%s），本次未消耗",

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
  provider  可选。取值: claude、codex、all。
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

	bgHintStart:          "启动: limitping bg start [claude|codex] [--dry-run]",
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

安装后，limitping 会在你正在使用时推迟 ping，并在一轮对话结束后恢复。未安装钩子时，Claude 改用 Claude Code 自己的会话列表（'claude agents'）判断，Codex 改用它自己的会话日志（~/.codex/sessions）判断。安装脚本会自动装好这些钩子；'limitping hooks status' 可检查它们是否仍在、是否最新，以及（Codex）是否已信任。`,
	hooksInstallShort: "在 Claude/Codex 配置中注册 limitping 的钩子",
	hooksInstallLong: `在 ~/.claude/settings.json 和 ~/.codex/hooks.json 中注册 limitping 的钩子（保留已有配置，并写入 .bak 备份）。

参数:
  provider  可选。取值: claude、codex、all。默认是 all。

Claude Code 会自动加载钩子；Codex 需要一次性信任：在 Codex 中运行 /hooks 启用它们。

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

	hooksStatusShort:     "检查 Claude/Codex 钩子是否已安装、是否最新，以及（Codex）是否已信任",
	hooksStatusLineFmt:   "%-7s %s  (%s)\n",
	hooksStateOff:        "未安装",
	hooksStateOK:         "已安装",
	hooksStateOutdated:   "需要更新",
	hooksStateMissing:    "已丢失",
	hooksMissingFmt:      "⚠ %s 钩子已从 %s 中消失（可能被其他工具改写）— 运行 `limitping hooks install %s`\n",
	hooksOutdatedFmt:     "⚠ %s 钩子不是最新 — 运行 `limitping hooks install %s`\n",
	hooksTrustUnknownFmt: "        无法向 Codex 确认是否已信任：%v\n",
	hooksUntrustedFmt:    "⚠ 以下钩子需要信任后 Codex 才会运行：%s — 在 Codex 中运行 /hooks\n",
	hooksTrusted:         "        Codex 已信任\n",

	updateAvailableFmt:      "\n\u2728 有新版本!  %s -> %s\n",
	updateNotesFmt:          "   更新说明: %s\n\n",
	updateOptionUpgrade:     "立即更新 (执行 `%s`)",
	updateOptionSkip:        "跳过",
	updateOptionSkipVersion: "跳过此版本",
	updateChooseHint:        "\u2191/\u2193 移动 \u00b7 Enter 确认 \u00b7 Esc 跳过",
	updateDismissedFmt:      "   已跳过 %s，下个版本会再提醒。\n",
	updateFailedFmt:         "   %v\n",
	updateRerunFmt:          "   已更新到 %s，请重新执行 `%s`。\n",
	upgradeShort:            "将 limitping 更新到最新版本",
	upgradeCurrentFmt:       "limitping %s 已是最新版本。\n",
	upgradeForceFlag:        "即使已是最新版本也强制重装",
	upgradeLong:             "下载适用于当前系统和架构的最新 GitHub Release，并替换正在运行的 limitping 二进制文件。",

	uninstallShort:      "删除 limitping 及其配置/缓存",
	uninstallLong:       "删除当前运行的 limitping 二进制文件及配置/缓存目录。使用 --keep-config 可保留配置/缓存文件。",
	uninstallKeepConfig: "保留 limitping 配置/缓存目录",
}
