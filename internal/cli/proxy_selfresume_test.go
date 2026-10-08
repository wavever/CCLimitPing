package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/provider"
)

func shortSelfResumeWait(t *testing.T, sessions func(context.Context) ([]provider.ClaudeSession, error)) {
	t.Helper()
	oldGrace, oldPoll, oldSessions := claudeSelfResumeGrace, claudeSelfResumePoll, claudeSessions
	claudeSelfResumeGrace, claudeSelfResumePoll, claudeSessions = 50*time.Millisecond, 5*time.Millisecond, sessions
	t.Cleanup(func() { claudeSelfResumeGrace, claudeSelfResumePoll, claudeSessions = oldGrace, oldPoll, oldSessions })
}

func noSessions(context.Context) ([]provider.ClaudeSession, error) {
	return nil, errors.New("unavailable")
}

func TestLimitDetectorSeesClaudeResumeAnnouncements(t *testing.T) {
	d := &limitDetector{sig: &limitSignal{}}
	_, _ = d.Write([]byte("\x1b[2mUsage limit has reset · \x1b[0mcontinuing automatically\n"))
	if !d.nativeContinued() {
		t.Fatal("Claude Code's resume line should be recognized")
	}
	d.reset()
	if d.nativeContinued() {
		t.Fatal("reset must forget the announcement, or the next limit would never be continued")
	}
	_, _ = d.Write([]byte("Automatic continue was turned off · this task will not resume on its own\n"))
	if !d.nativeDeclined() || d.nativeContinued() {
		t.Fatal("the give-up line should count as declined, not continued")
	}
}

// The model says "continuing now" in ordinary replies; only Claude Code's own
// limit line may count, or the proxy would never type the message later.
func TestLimitDetectorIgnoresTheModelSayingContinuing(t *testing.T) {
	d := &limitDetector{sig: &limitSignal{}}
	_, _ = d.Write([]byte("⏺ Tests pass. Continuing now with step 3, continuing automatically through the rest.\n"))
	if d.nativeContinued() {
		t.Fatal("assistant prose was taken for Claude Code resuming the task")
	}
	// The TUI draws spaces as cursor moves, which cleaning deletes outright.
	_, _ = d.Write([]byte("Usage\x1b[1Climit\x1b[1Creset\x1b[1C·\x1b[1Ccontinuing\x1b[1Cautomatically"))
	if !d.nativeContinued() {
		t.Fatal("Claude Code's resume line with cursor-move spacing was missed")
	}
	d.forgetNative()
	if d.nativeContinued() {
		t.Fatal("forgetNative must clear the signal")
	}
}

func TestClaudeSelfResumedOnAnnouncement(t *testing.T) {
	shortSelfResumeWait(t, noSessions)
	d := &limitDetector{sig: &limitSignal{}}
	_, _ = d.Write([]byte("Usage limit available again · continuing now"))
	if !claudeSelfResumed(context.Background(), d, 42, nil) {
		t.Fatal("an announced resume must stop the proxy from typing the message too")
	}
}

func TestClaudeSelfResumedWhenItsSessionIsBusy(t *testing.T) {
	shortSelfResumeWait(t, func(context.Context) ([]provider.ClaudeSession, error) {
		return []provider.ClaudeSession{
			{PID: 7, Status: "busy"}, // somebody else's session
			{PID: 42, Status: "busy"},
		}, nil
	})
	if !claudeSelfResumed(context.Background(), &limitDetector{sig: &limitSignal{}}, 42, nil) {
		t.Fatal("this session running a turn means it resumed")
	}
}

func TestClaudeNotSelfResumedOnlyBecauseAnotherSessionIsBusy(t *testing.T) {
	shortSelfResumeWait(t, func(context.Context) ([]provider.ClaudeSession, error) {
		return []provider.ClaudeSession{{PID: 7, Status: "busy"}, {PID: 42, Status: "idle"}}, nil
	})
	if claudeSelfResumed(context.Background(), &limitDetector{sig: &limitSignal{}}, 42, nil) {
		t.Fatal("another session's turn says nothing about this one")
	}
}

func TestClaudeNotSelfResumedWhenItGivesUp(t *testing.T) {
	shortSelfResumeWait(t, noSessions)
	claudeSelfResumeGrace = time.Hour // the give-up line must short-circuit the wait
	d := &limitDetector{sig: &limitSignal{}}
	_, _ = d.Write([]byte("Automatic continue stopped · this task will not resume on its own"))
	done := make(chan bool, 1)
	go func() { done <- claudeSelfResumed(context.Background(), d, 42, nil) }()
	select {
	case resumed := <-done:
		if resumed {
			t.Fatal("declined must not count as resumed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waited out the grace period although Claude Code said it will not resume")
	}
}

func TestRotateLogKeepsOnePreviousBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "continue.log")
	if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	rotateLog(path, 100)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a log under the cap must stay put")
	}
	rotateLog(path, 5)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a log over the cap must be moved aside")
	}
	if b, _ := os.ReadFile(path + ".1"); string(b) != "0123456789" {
		t.Fatalf("rotated copy = %q", b)
	}
}

func TestClaudeNotSelfResumedAfterGrace(t *testing.T) {
	shortSelfResumeWait(t, noSessions)
	if claudeSelfResumed(context.Background(), &limitDetector{sig: &limitSignal{}}, 42, nil) {
		t.Fatal("silence (e.g. a Claude Code that cannot resume) must fall back to typing the message")
	}
}
