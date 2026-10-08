//go:build !windows

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/wavever/CCLimitPing/internal/config"
)

// runContinueProxy launches providerName's interactive CLI (plus any passthrough
// extraArgs, e.g. --yolo) under a PTY, wiring the user's terminal straight
// through, while watchAndContinue injects the continue message whenever the 5h
// limit recovers after being hit. providerName must be "claude" or "codex".
func runContinueProxy(ctx context.Context, out io.Writer, providerName string, extraArgs []string, cfg config.Config) error {
	p, err := buildProvider(providerName, cfg)
	if err != nil {
		return err
	}
	msg := continueMessage(cfg, providerName)

	lg := newProxyLogger()
	defer lg.close()

	// SIGTERM tears the child down so the deferred terminal restore runs. We do
	// NOT trap SIGINT: raw mode forwards Ctrl-C to the child as a byte, which is
	// what an interactive CLI expects.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer stop()

	label := providerName
	if len(extraArgs) > 0 {
		label += " " + strings.Join(extraArgs, " ")
	}
	fmt.Fprintf(out, localizedText().continueStartedFmt, label, msg)

	cwd, _ := os.Getwd()
	lg.logf("started: provider=%s args=%v cwd=%s continue=%q", providerName, extraArgs, cwd, msg)

	cmd := exec.CommandContext(ctx, providerName, extraArgs...) // plain CLI; the user drives it
	// On cancellation ask the CLI to exit gracefully (restore its terminal
	// state, save session state) instead of exec's default SIGKILL; escalate to
	// a kill only after WaitDelay.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	ptmx, err := pty.Start(cmd)
	if err != nil {
		lg.logf("failed to start %s: %v", providerName, err)
		return fmt.Errorf("starting %s: %w", providerName, err)
	}
	defer func() { _ = ptmx.Close() }()

	// Keep the PTY the same size as the real terminal.
	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)
	go func() {
		for range resize {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	resize <- syscall.SIGWINCH // set the initial size

	// Raw mode so keystrokes (Ctrl-C, arrows, paste, …) reach the child verbatim.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if old, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
			defer func() { _ = term.Restore(int(os.Stdin.Fd()), old) }()
		}
	}

	// Pump I/O both ways; watch usage and auto-continue in the background. The
	// child's output is also teed through limitDetector so the on-screen limit
	// message (e.g. "You've hit your session limit") corroborates the usage
	// endpoint when deciding the session is parked. All synthetic keystrokes
	// (continue message, question confirmation) go through one sessionInjector
	// so they cannot interleave.
	inject := &sessionInjector{w: ptmx}
	var questionConfirmer *claudeLimitQuestionConfirmer
	if providerName == "claude" {
		questionConfirmer = &claudeLimitQuestionConfirmer{inject: inject, lg: lg}
	}
	detector := &limitDetector{sig: &limitSignal{}, lg: lg, claudeQuestion: questionConfirmer, scanEvery: proxyScanThrottle}
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.MultiWriter(os.Stdout, detector), ptmx)
		close(copyDone)
	}()
	var selfResumed func(context.Context) bool
	if providerName == "claude" {
		pid := cmd.Process.Pid
		selfResumed = func(ctx context.Context) bool { return claudeSelfResumed(ctx, detector, pid, lg) }
	}
	go watchAndContinue(ctx, p, inject, msg, cfg, detector, lg, selfResumed)

	err = cmd.Wait()
	// Give the output pump a moment to drain the child's final bytes (incl. the
	// alternate-screen exit sequence) before the deferred ptmx.Close cuts it
	// off; bounded in case a grandchild still holds the PTY slave open.
	select {
	case <-copyDone:
	case <-time.After(500 * time.Millisecond):
	}
	lg.logf("exit: %v", err)
	return err
}
