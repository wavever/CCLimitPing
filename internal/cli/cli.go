// Package cli wires up the limitping command-line interface.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/scheduler"
)

// Version is set at build time via -ldflags by the release pipeline. Leaving it
// empty everywhere else is deliberate: version() then derives the value from
// the module's own build info, so the tag is the single source of truth and
// there is no second copy to forget to bump. DevVersion marks a build that came
// from neither, which is any local `go build`.
var Version = ""

// DevVersion is reported for a build with no release identity. It is not a
// version number on purpose, so it can never compare as newer or older than a
// real release.
const DevVersion = "dev"

// version resolves the running binary's version for display: the release
// ldflag, else the module version recorded by `go install pkg@version`, else
// DevVersion tagged with the revision it was built from.
func version() string {
	if Version != "" {
		return strings.TrimPrefix(Version, "v")
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return DevVersion
	}
	if v := releaseVersion(bi.Main.Version); v != "" {
		return v
	}
	if rev := vcsRevision(bi); rev != "" {
		return DevVersion + "+" + rev
	}
	return DevVersion
}

// isReleaseVersion reports whether the binary knows which published release it
// is. A local build does not, so it must not be compared against the newest
// release — there is nothing meaningful to say about whether it is out of date.
func isReleaseVersion() bool { return releaseVersion(version()) != "" }

var releaseVersionRE = regexp.MustCompile(`^\d+(\.\d+)*$`)

// releaseVersion returns v without its leading "v" when it names a published
// release — bare numeric components and nothing else. A `go build` in a work
// tree reports a pseudo-version instead (0.9.1-0.20260909072312-7599547+dirty),
// which names no release, and neither does a pre-release or snapshot ldflag.
func releaseVersion(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if !releaseVersionRE.MatchString(v) {
		return ""
	}
	return v
}

// vcsRevision is the short commit a local build came from, plus a dirty marker
// when the work tree had uncommitted changes. Empty when Go recorded no VCS
// information (building outside a repository, or with -buildvcs=false).
func vcsRevision(bi *debug.BuildInfo) string {
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return ""
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

// BinaryAlias is the short name installed alongside the binary, so `lmp` is
// interchangeable with `limitping`. install.sh creates it as a symlink and
// `uninstall` removes it. It deliberately avoids the `lp*` namespace, which
// belongs to CUPS: `lp`, `lpq`, `lpr`, `lprm`, `lpstat` and friends ship on
// macOS and most Linux distributions, and installing into a directory that
// precedes /usr/bin on PATH would shadow the system printing commands.
const BinaryAlias = "lmp"

// invokedName is the name limitping was called as, so usage lines echo back the
// command the user actually typed. Only the alias is recognized: any other name
// (a renamed binary, the test runner) reads as limitping.
func invokedName() string {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == BinaryAlias {
		return BinaryAlias
	}
	return "limitping"
}

// alternateName is the name limitping was not invoked as, advertised in the
// root help so both remain discoverable from either one.
func alternateName() string {
	if invokedName() == BinaryAlias {
		return "limitping"
	}
	return BinaryAlias
}

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	text := localizedText()
	root := &cobra.Command{
		Use: invokedName(),
		// Purely informational — the root has no parent to resolve an alias
		// through. It makes `--help` render "Aliases: lmp, limitping" through
		// the same section that already advertises per-command aliases, so
		// whichever name you typed, the other one is discoverable.
		Aliases:       []string{alternateName()},
		Short:         text.rootShort,
		Long:          text.rootLong,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Runs ahead of every subcommand; no subcommand defines its own
		// PersistentPreRun, which would shadow this one.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			if !skipsUpdateNotice(cmd) {
				updateNotice(cmd.Context(), cmd.OutOrStdout(), text, os.Stdin)
			}
		},
	}
	if text.usageTemplate != "" {
		root.SetUsageTemplate(text.usageTemplate)
	}
	root.AddCommand(newStatusCmd(), newPingCmd(), newWatchCmd(), newScheduleCmd(), newContinueCmd(), newRedeemCmd(), newBackgroundCmd(), newConfigCmd(), newHooksCmd(), newHookCmd(), newUpgradeCmd(), newUninstallCmd(), newVersionCmd())
	localizeCompletionCommand(root, text)
	root.SetHelpCommand(newHelpCommand(text))
	localizeHelpFlags(root, text)
	localizeInvocations(root)
	return root
}

// localizeInvocations rewrites the `limitping <command>` examples in the help
// text to whichever name the binary was invoked as, so they stay copy-pasteable
// under the alias. Only invocations are touched: a bare "limitping" is the
// product name ("limitping watches usage in the background") and stays put.
// A no-op unless the alias was used.
func localizeInvocations(root *cobra.Command) {
	name := invokedName()
	if name == "limitping" {
		return
	}
	// Execute() attaches the help command; do it now so `limitping help` in the
	// usage footer counts as an invocation. Idempotent, and Execute repeats it.
	root.InitDefaultHelpCmd()
	pattern := invocationPattern(root)
	rewrite := func(s string) string {
		if s == "" {
			return s
		}
		return pattern.ReplaceAllString(s, name+" $1")
	}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.Short = rewrite(cmd.Short)
		cmd.Long = rewrite(cmd.Long)
		cmd.Example = rewrite(cmd.Example)
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)

	// The usage footer ("Type limitping help [command] ...") lives in the
	// template, which children inherit from the root.
	if tmpl := rewrite(root.UsageTemplate()); tmpl != root.UsageTemplate() {
		root.SetUsageTemplate(tmpl)
	}
}

// invocationPattern matches "limitping " followed by a real command name or
// alias from the tree (at a word boundary, so the prose "limitping watches" is
// left alone), plus the flag forms.
func invocationPattern(root *cobra.Command) *regexp.Regexp {
	seen := map[string]bool{}
	tokens := []string{`--?\w[\w-]*`} // --help, -h
	var collect func(*cobra.Command)
	collect = func(cmd *cobra.Command) {
		for _, child := range cmd.Commands() {
			for _, token := range append([]string{child.Name()}, child.Aliases...) {
				if !seen[token] {
					seen[token] = true
					tokens = append(tokens, regexp.QuoteMeta(token))
				}
			}
			collect(child)
		}
	}
	collect(root)
	return regexp.MustCompile(`\blimitping ((?:` + strings.Join(tokens, "|") + `)\b)`)
}

func newHelpCommand(text cliText) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "help [command]",
		Short: text.helpCommandShort,
		Long:  text.helpCommandLong,
		Run: func(cmd *cobra.Command, args []string) {
			target, _, err := cmd.Root().Find(args)
			if target == nil || err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s %#q\n", text.helpUnknownTopic, args)
				_ = cmd.Root().Usage()
				return
			}
			target.InitDefaultHelpFlag()
			localizeHelpFlag(target, text)
			_ = target.Help()
		},
	}
	cmd.InitDefaultHelpFlag()
	localizeHelpFlag(cmd, text)
	return cmd
}

func localizeHelpFlags(cmd *cobra.Command, text cliText) {
	cmd.InitDefaultHelpFlag()
	localizeHelpFlag(cmd, text)
	for _, child := range cmd.Commands() {
		localizeHelpFlags(child, text)
	}
}

func localizeHelpFlag(cmd *cobra.Command, text cliText) {
	if flag := cmd.Flags().Lookup("help"); flag != nil {
		flag.Usage = text.helpFlag
	}
}

func localizeCompletionCommand(root *cobra.Command, text cliText) {
	root.InitDefaultCompletionCmd()
	cmd := findChildCommand(root, "completion")
	if cmd == nil {
		return
	}
	cmd.Short = text.completionShort
	cmd.Long = text.completionLong

	for _, child := range cmd.Commands() {
		child.Short = fmt.Sprintf(text.completionShellShort, child.Name())
		child.Long = fmt.Sprintf(text.completionShellLong, child.Name())
		if flag := child.Flags().Lookup("no-descriptions"); flag != nil {
			flag.Usage = text.completionNoDescFlag
		}
	}
}

func findChildCommand(parent *cobra.Command, name string) *cobra.Command {
	for _, child := range parent.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

func newVersionCmd() *cobra.Command {
	text := localizedText()
	return &cobra.Command{
		Use:     "version",
		Aliases: []string{"v", "ver"},
		Short:   text.versionShort,
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "limitping %s\n", version())

			// An upgrade is driven by the binary being replaced, so a version
			// that predates the alias installs a binary whose --help advertises
			// `lmp` without ever creating one — and the user has no way to know
			// a second `upgrade` would fix it. Every released `upgrade` ends by
			// running `<new binary> version`, which makes this the one place a
			// new build gets to act on its own behalf after an old one upgraded
			// it. Cheap (a readlink), idempotent, and bound by the same
			// ownership rules as install.sh. It reports on stderr so `version`
			// stdout stays exactly one parseable line.
			if exe, err := currentExecutable(); err == nil {
				ensureAlias(exe, cmd.ErrOrStderr())
			}
		},
	}
}

// buildProvider constructs a single provider from config.
func buildProvider(name string, cfg config.Config) (provider.Provider, error) {
	switch name {
	case "claude":
		return provider.NewClaude(cfg.Claude), nil
	case "codex":
		return provider.NewCodex(cfg.Codex), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (want claude, codex, or all)", name)
	}
}

// enabledProviders returns the providers marked enabled in config.
func enabledProviders(cfg config.Config) []provider.Provider {
	var ps []provider.Provider
	if cfg.Claude.Enabled {
		ps = append(ps, provider.NewClaude(cfg.Claude))
	}
	if cfg.Codex.Enabled {
		ps = append(ps, provider.NewCodex(cfg.Codex))
	}
	return ps
}

// selectProviders resolves the --provider flag value to a provider set. "all"
// (or empty) returns the enabled providers; a specific name returns that one
// even if disabled (explicit override).
func selectProviders(cfg config.Config, name string) ([]provider.Provider, error) {
	if name == "" || name == "all" {
		ps := enabledProviders(cfg)
		if len(ps) == 0 {
			return nil, fmt.Errorf("no providers enabled in config")
		}
		return ps, nil
	}
	p, err := buildProvider(name, cfg)
	if err != nil {
		return nil, err
	}
	return []provider.Provider{p}, nil
}

// makeTarget pairs a provider with the scheduling options from its config
// section.
func makeTarget(p provider.Provider, pc config.ProviderConfig) (scheduler.Target, error) {
	var anchor time.Time
	if pc.AlignStart != "" {
		t, err := time.Parse(time.RFC3339, pc.AlignStart)
		if err != nil {
			return scheduler.Target{}, fmt.Errorf("%s align_start: %w", p.Name(), err)
		}
		anchor = t
	}
	return scheduler.Target{Provider: p, AlignStart: anchor, AutoRedeem: pc.AutoRedeem}, nil
}

// buildTargets builds scheduler targets for all enabled providers.
func buildTargets(cfg config.Config) ([]scheduler.Target, error) {
	var targets []scheduler.Target
	add := func(p provider.Provider, pc config.ProviderConfig) error {
		t, err := makeTarget(p, pc)
		if err != nil {
			return err
		}
		targets = append(targets, t)
		return nil
	}
	if cfg.Claude.Enabled {
		if err := add(provider.NewClaude(cfg.Claude), cfg.Claude); err != nil {
			return nil, err
		}
	}
	if cfg.Codex.Enabled {
		if err := add(provider.NewCodex(cfg.Codex), cfg.Codex); err != nil {
			return nil, err
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no providers enabled in config")
	}
	return targets, nil
}

// selectTargets resolves a provider name to scheduler targets. "all" (or empty)
// returns targets for every enabled provider; a specific name returns just that
// one, even if it's disabled in config (an explicit override, matching `ping`).
func selectTargets(cfg config.Config, name string) ([]scheduler.Target, error) {
	if name == "" || name == "all" {
		return buildTargets(cfg)
	}
	p, err := buildProvider(name, cfg)
	if err != nil {
		return nil, err
	}
	t, err := makeTarget(p, providerConfig(cfg, name))
	if err != nil {
		return nil, err
	}
	return []scheduler.Target{t}, nil
}

// providerConfig returns the config section for a provider name.
func providerConfig(cfg config.Config, name string) config.ProviderConfig {
	switch name {
	case "claude":
		return cfg.Claude
	case "codex":
		return cfg.Codex
	}
	return config.ProviderConfig{}
}
