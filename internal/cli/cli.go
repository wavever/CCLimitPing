// Package cli wires up the limitping command-line interface.
package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/scheduler"
)

// Version is the binary version, overridable at build time via -ldflags.
var Version = "0.5.0"

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	text := localizedText()
	root := &cobra.Command{
		Use:           "limitping",
		Short:         text.rootShort,
		Long:          text.rootLong,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	if text.usageTemplate != "" {
		root.SetUsageTemplate(text.usageTemplate)
	}
	root.AddCommand(newStatusCmd(), newPingCmd(), newWatchCmd(), newBackgroundCmd(), newConfigCmd(), newHooksCmd(), newHookCmd(), newUpgradeCmd(), newUninstallCmd(), newVersionCmd())
	localizeCompletionCommand(root, text)
	root.SetHelpCommand(newHelpCommand(text))
	localizeHelpFlags(root, text)
	return root
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
			fmt.Fprintf(cmd.OutOrStdout(), "limitping %s\n", Version)
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
	case "spark":
		return provider.NewSpark(cfg.Spark), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (want claude, codex, spark, or all)", name)
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
	if cfg.Spark.Enabled {
		ps = append(ps, provider.NewSpark(cfg.Spark))
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

// makeTarget pairs a provider with its parsed align_start anchor.
func makeTarget(p provider.Provider, alignStart string) (scheduler.Target, error) {
	var anchor time.Time
	if alignStart != "" {
		t, err := time.Parse(time.RFC3339, alignStart)
		if err != nil {
			return scheduler.Target{}, fmt.Errorf("%s align_start: %w", p.Name(), err)
		}
		anchor = t
	}
	return scheduler.Target{Provider: p, AlignStart: anchor}, nil
}

// buildTargets builds scheduler targets for all enabled providers.
func buildTargets(cfg config.Config) ([]scheduler.Target, error) {
	var targets []scheduler.Target
	add := func(p provider.Provider, alignStart string) error {
		t, err := makeTarget(p, alignStart)
		if err != nil {
			return err
		}
		targets = append(targets, t)
		return nil
	}
	if cfg.Claude.Enabled {
		if err := add(provider.NewClaude(cfg.Claude), cfg.Claude.AlignStart); err != nil {
			return nil, err
		}
	}
	if cfg.Codex.Enabled {
		if err := add(provider.NewCodex(cfg.Codex), cfg.Codex.AlignStart); err != nil {
			return nil, err
		}
	}
	if cfg.Spark.Enabled {
		if err := add(provider.NewSpark(cfg.Spark), cfg.Spark.AlignStart); err != nil {
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
	t, err := makeTarget(p, providerAlignStart(cfg, name))
	if err != nil {
		return nil, err
	}
	return []scheduler.Target{t}, nil
}

// providerAlignStart returns the configured align_start for a provider name.
func providerAlignStart(cfg config.Config, name string) string {
	switch name {
	case "claude":
		return cfg.Claude.AlignStart
	case "codex":
		return cfg.Codex.AlignStart
	case "spark":
		return cfg.Spark.AlignStart
	}
	return ""
}
