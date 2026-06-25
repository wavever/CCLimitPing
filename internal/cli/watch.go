package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/scheduler"
)

func newWatchCmd() *cobra.Command {
	var dryRun bool
	var live bool
	text := localizedText()
	cmd := &cobra.Command{
		Use:       "watch [provider]",
		Aliases:   []string{"w"},
		Short:     text.watchShort,
		Long:      text.watchLong,
		Args:      cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"claude", "codex", "spark", "all"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name := "all"
			if len(args) > 0 {
				name = args[0]
			}
			targets, err := selectTargets(cfg, name)
			if err != nil {
				return err
			}
			release, err := acquireWatchLock(name, dryRun)
			if err != nil {
				return err
			}
			defer release()

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			s := scheduler.New(cfg, targets, dryRun, live, cmd.OutOrStdout())
			s.Run(ctx)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, text.watchDryRunFlag)
	cmd.Flags().BoolVar(&live, "live", false, text.watchLiveFlag)
	return cmd
}
