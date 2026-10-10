package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
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
		ValidArgs: []string{"claude", "codex", "all"},
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
			return runPings(cmd.Context(), cmd.OutOrStdout(), text, providers,
				dryRun, isTerminal(os.Stdout), cfg.UsageDisplay)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, text.pingDryRunFlag)
	return cmd
}

// runPings triggers each provider and then reports the window state the pings
// were meant to change. Reading usage is free and never starts a window, and
// without it the command only answers "the request went out" while the question
// actually being asked is "did my window start" — which the ping's own output
// cannot show, because a ping is far too small to move the used percentage.
func runPings(ctx context.Context, out io.Writer, text cliText, providers []provider.Provider, dryRun, tty bool, display string) error {
	var firstErr error
	for _, p := range providers {
		if err := runPing(ctx, out, text, p, dryRun, tty); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if dryRun {
		return firstErr // nothing was sent, so there is no new state to report
	}
	fmt.Fprintln(out)
	// Reported even when a ping failed: that is exactly when the window state
	// is worth seeing. A failing status read is printed inline by runStatus and
	// must not turn a successful ping into a failed command.
	_ = runStatus(ctx, out, io.Discard, text, providers, false, false, display, false)
	return firstErr
}

// runPing triggers one provider with live feedback so the user can see what the
// CLI is doing during the (often multi-second) shell-out.
func runPing(parent context.Context, out io.Writer, text cliText, p provider.Provider, dryRun, tty bool) error {
	name := p.Name()

	// Resolve the exact command first (a dry-run Trigger executes nothing).
	dry, err := p.Trigger(parent, true)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(out, text.pingWouldRunFmt, name, commandLine(text, dry))
		return nil
	}

	fmt.Fprintf(out, "%-7s → %s\n", name, commandLine(text, dry))

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
		report(out, text, name, start, o.res, o.err)
		return o.err
	}

	frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case o := <-done:
			fmt.Fprint(out, eraseLine) // clear the spinner line
			report(out, text, name, start, o.res, o.err)
			return o.err
		case <-ticker.C:
			fmt.Fprintf(out, text.pingSendingFmt, name, frames[i%len(frames)], elapsed(start))
			i++
		}
	}
}

// commandLine renders the command to be run, naming the model when the command
// itself doesn't. limitping only passes -m/--model when one is configured; with
// it unset the CLI picks the model, and the bare command would leave the user
// unable to tell which model the ping just spent quota on.
func commandLine(text cliText, res *provider.TriggerResult) string {
	if res.Model == "" || strings.Contains(res.Command, res.Model) {
		return res.Command
	}
	return res.Command + fmt.Sprintf(text.pingModelFmt, res.Model)
}

func report(out io.Writer, text cliText, name string, start time.Time, res *provider.TriggerResult, err error) {
	if err != nil {
		fmt.Fprintf(out, text.pingFailedFmt, name, elapsed(start), localizedProviderError(text, err))
		return
	}
	fmt.Fprintf(out, text.pingSuccessFmt, name, elapsed(start), usageSuffix(res))
}

// usageSuffix renders the token/cost tail, e.g. ", 32,934 tokens, $0.0110".
func usageSuffix(res *provider.TriggerResult) string {
	if res == nil || !res.HasUsage {
		return ""
	}
	s := fmt.Sprintf(", %s tokens (in %s / out %s)",
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
