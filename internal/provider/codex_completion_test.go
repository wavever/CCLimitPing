package provider

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
)

func TestCodexCompletionOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, script, wantError string
		completed               bool
	}{
		{"completed", `printf '\033]9;done\007'`, "", true},
		{"clean-without-marker", "exit 0", "completion unconfirmed", false},
		{"process-error", "exit 1", "interactive failed", false},
		{"completed-process-error", `printf '\033]9;done\007'; exit 1`, "interactive failed", true},
		{"timeout", "exec sleep 5", "timed out", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeCodexCLI(t, tc.script)
			res, err := triggerCodexWithTiming(context.Background(), config.ProviderConfig{}, false,
				codexInteractiveTiming{maxWait: 100 * time.Millisecond, exitGrace: 20 * time.Millisecond})
			if res.TurnCompleted != tc.completed {
				t.Fatalf("completed=%v", res.TurnCompleted)
			}
			var completionErr *CodexCompletionError
			if got, want := errors.As(err, &completionErr), tc.name == "timeout" || tc.name == "clean-without-marker"; got != want {
				t.Fatalf("startup diagnostic classification=%v, want %v: %v", got, want, err)
			}
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatal(err)
			}
		})
	}
}

func TestCodexCompletionAndFailureReadyTogether(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix subprocess exit semantics")
	}
	exitErr := exec.Command("sh", "-c", "exit 1").Run()
	if exitErr == nil {
		t.Fatal("expected subprocess failure")
	}
	for i := 0; i < 100; i++ {
		completed, readDone := make(chan struct{}), make(chan struct{})
		close(completed)
		close(readDone)
		done := make(chan error, 1)
		done <- exitErr
		output := &limitedBuffer{limit: 4096}
		terminal, confirmed, err := codexAwait(context.Background(), nil, nil, output, completed, readDone, done, time.Second)
		if !terminal {
			err = codexInteractiveStop(context.Background(), nil, nil, done, output, time.Second)
		}
		if !confirmed || err == nil || !strings.Contains(err.Error(), "interactive failed") {
			t.Fatal(terminal, confirmed, err)
		}
	}
}

func TestCodexShutdownExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Unix subprocess signal semantics")
	}
	for _, tc := range []struct {
		script string
		failed bool
	}{
		{"exit 0", false},
		{"exit 1", true},
		{"exit 130", false},
		{"kill -INT $$", false},
		{"kill -TERM $$", true},
	} {
		t.Run(tc.script, func(t *testing.T) {
			err := exec.Command("sh", "-c", tc.script).Run()
			if got := codexShutdownErr(err, &limitedBuffer{limit: 4096}); (got != nil) != tc.failed {
				t.Fatal(got)
			}
		})
	}
}

func TestCodexCompletionCancellation(t *testing.T) {
	fakeCodexCLI(t, "exec sleep 5")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	res, err := triggerCodexWithTiming(ctx, config.ProviderConfig{}, false,
		codexInteractiveTiming{maxWait: time.Second, exitGrace: 20 * time.Millisecond})
	if err == nil || res.TurnCompleted {
		t.Fatal(res, err)
	}
}

func TestCodexAwaitDrainsCompletionAfterProcessExit(t *testing.T) {
	markers := newCodexTurnMarkers()
	readDone := make(chan struct{})
	done := make(chan error, 1)
	done <- nil
	go func() {
		time.Sleep(5 * time.Millisecond)
		_, _ = markers.Write([]byte("\x1b]9;complete\x07"))
		close(readDone)
	}()
	terminal, completed, err := codexAwait(context.Background(), nil, nil, &limitedBuffer{limit: 4096},
		markers.completed, readDone, done, time.Second)
	if !terminal || !completed || err != nil {
		t.Fatal(terminal, completed, err)
	}
}

func TestCodexCompletionMarkerFragments(t *testing.T) {
	m := newCodexTurnMarkers()
	for _, fragment := range []string{"noise\x1b]", "9;", "finished", "\a"} {
		_, _ = m.Write([]byte(fragment))
	}
	select {
	case <-m.completed:
	default:
		t.Fatal("fragmented notification not detected")
	}
}
