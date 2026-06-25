package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/wavever/CCLimitPing/internal/config"
	"github.com/wavever/CCLimitPing/internal/provider"
)

func newPingCmd() *cobra.Command {
	var dryRun bool
	text := localizedText()
	cmd := &cobra.Command{
		Use:       "ping [provider]",
		Aliases:   []string{"p"},
		Short:     text.pingShort,
		Long:      text.pingLong,
		Args:      cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"claude", "codex", "spark", "all"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "all"
			if len(args) > 0 {
				name = args[0]
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			providers, err := selectProviders(cfg, name)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			tty := isTerminal(os.Stdout)
			var firstErr error
			for _, p := range providers {
				if err := runPing(cmd.Context(), out, p, dryRun, tty); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, text.pingDryRunFlag)
	return cmd
}

// runPing triggers one provider with live feedback so the user can see what the
// CLI is doing during the (often multi-second) shell-out.
func runPing(parent context.Context, out io.Writer, p provider.Provider, dryRun, tty bool) error {
	name := p.Name()

	// Resolve the exact command first (a dry-run Trigger executes nothing).
	dry, err := p.Trigger(parent, true)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(out, "%-7s would run: %s\n", name, dry.Command)
		return nil
	}

	fmt.Fprintf(out, "%-7s → %s\n", name, dry.Command)

	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()

	start := time.Now()
	type outcome struct {
		res *provider.TriggerResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := p.Trigger(ctx, false)
		done <- outcome{res, err}
	}()

	if !tty {
		// No spinner on non-terminals; just wait and report.
		o := <-done
		report(out, name, start, o.res, o.err)
		return o.err
	}

	frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case o := <-done:
			fmt.Fprint(out, "\r\033[K") // clear the spinner line
			report(out, name, start, o.res, o.err)
			return o.err
		case <-ticker.C:
			fmt.Fprintf(out, "\r%-7s %c sending… %s", name, frames[i%len(frames)], elapsed(start))
			i++
		}
	}
}

func report(out io.Writer, name string, start time.Time, res *provider.TriggerResult, err error) {
	if err != nil {
		fmt.Fprintf(out, "%-7s ✗ failed after %s: %v\n", name, elapsed(start), err)
		return
	}
	fmt.Fprintf(out, "%-7s ✓ pinged (%s%s)\n", name, elapsed(start), usageSuffix(res))
}

// usageSuffix renders the token/cost tail, e.g. ", 32,934 tok, $0.0110".
func usageSuffix(res *provider.TriggerResult) string {
	if res == nil || !res.HasUsage {
		return ""
	}
	s := fmt.Sprintf(", %s tok (in %s / out %s)",
		humanInt(res.TotalTokens), humanInt(res.InputTokens), humanInt(res.OutputTokens))
	if res.CostUSD > 0 {
		s += fmt.Sprintf(", $%.4f", res.CostUSD)
	}
	return s
}

// humanInt formats an int with thousands separators.
func humanInt(n int) string {
	s := fmt.Sprintf("%d", n)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	var b []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			b = append(b, ',')
		}
		b = append(b, c)
	}
	return neg + string(b)
}

func elapsed(start time.Time) string {
	return time.Since(start).Truncate(100 * time.Millisecond).String()
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
