package codexstate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/usage"
)

var epoch = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func quota(at, reset time.Time, used float64, weekly bool) *usage.Usage {
	u := &usage.Usage{Plan: "pro", FetchedAt: at}
	w := usage.Window{UsedPercent: used, ResetsAt: reset, WindowSeconds: 18000}
	if weekly {
		w.WindowSeconds = 604800
		u.Weekly = w
	} else {
		u.FiveHour = w
	}
	return u
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		name      string
		gap, move time.Duration
		used      float64
		want      string
	}{
		{"first-minute", 59 * time.Second, 0, 0, Unknown},
		{"fixed", time.Minute, 0, 0, Started},
		{"fixed-six-minutes", 6 * time.Minute, 0, 0, Started},
		{"fixed-hours", 2 * time.Hour, 0, 0, Started},
		{"sliding", time.Minute, time.Minute, 0, NotStarted},
		{"jitter", time.Minute, time.Second, 0, Started},
		{"ambiguous", time.Minute, 30 * time.Second, 0, Unknown},
		{"stale-sliding", 11 * time.Minute, 11 * time.Minute, 0, Unknown},
		{"positive", time.Second, 0, 1, Started},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &window{}
			reset := epoch.Add(5 * time.Hour)
			observe(w, Sample{At: epoch, Reset: reset, Seconds: 18000}, false)
			got := observe(w, Sample{At: epoch.Add(tc.gap), Reset: reset.Add(tc.move), Seconds: 18000, Used: tc.used}, false)
			if got.State != tc.want {
				t.Fatalf("got %s want %s", got.State, tc.want)
			}
		})
	}
}

func TestPollingAndInvalidation(t *testing.T) {
	w := &window{}
	reset := epoch.Add(5 * time.Hour)
	for i := 0; i <= 60; i += 10 {
		v := observe(w, Sample{At: epoch.Add(time.Duration(i) * time.Second), Reset: reset, Seconds: 18000}, false)
		if i == 60 && v.State != Started {
			t.Fatal(v)
		}
	}
	for _, s := range []Sample{
		{At: epoch.Add(61 * time.Second), Reset: reset, Seconds: 604800},
		{At: epoch.Add(-time.Second), Reset: reset, Seconds: 18000},
		{At: reset.Add(time.Second), Reset: reset, Seconds: 18000},
	} {
		copy := *w
		if v := observe(&copy, s, false); v.State != Unknown {
			t.Fatal(v)
		}
	}
}

func read(t *testing.T, s Store, key string, u *usage.Usage) *usage.Verification {
	t.Helper()
	v, err := s.Observe("account", key, u)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPostAttemptBaselineAndExistingStarted(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{true: "already-started", false: "new-start"}[started], func(t *testing.T) {
			s := Store{Dir: t.TempDir()}
			reset := epoch.Add(5 * time.Hour)
			read(t, s, "codex", quota(epoch, reset, 0, false))
			if !started {
				reset = reset.Add(time.Minute)
			}
			pre := quota(epoch.Add(time.Minute), reset, 0, false)
			read(t, s, "codex", pre)
			id, err := s.Begin("account", "codex", false, pre.FetchedAt, pre.FetchedAt)
			if err != nil {
				t.Fatal(err)
			}
			end := pre.FetchedAt.Add(5 * time.Second)
			if err := s.Finish("account", "codex", id, end, false); err != nil {
				t.Fatal(err)
			}
			if !started {
				reset = end.Add(5 * time.Hour)
			}
			v := read(t, s, "codex", quota(end, reset, 0, false))
			want := Unknown
			if started {
				want = Started
			}
			if v.FiveHour.State != want {
				t.Fatal(v)
			}
			v = read(t, s, "codex", quota(end.Add(time.Minute), reset, 0, false))
			if v.FiveHour.State != Started {
				t.Fatal(v)
			}
		})
	}
}

func TestBucketIdentityAndTarget(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, key := range []string{"codex", "spark:model"} {
		u := quota(epoch, epoch.Add(5*time.Hour), 0, false)
		u.Weekly = usage.Window{UsedPercent: 5, ResetsAt: epoch.Add(7 * 24 * time.Hour), WindowSeconds: 604800}
		v := read(t, s, key, u)
		if v.Target != "five_hour" || v.Recovery == "window_started" {
			t.Fatal(v)
		}
	}
	v := read(t, s, "codex", quota(epoch.Add(time.Minute), epoch.Add(5*time.Hour), 0, false))
	if v.FiveHour.State != Started {
		t.Fatal(v)
	}
	v = read(t, s, "spark:model", quota(epoch.Add(time.Minute), epoch.Add(5*time.Hour+time.Minute), 0, false))
	if v.FiveHour.State != NotStarted {
		t.Fatal(v)
	}
	u := quota(epoch, epoch.Add(7*24*time.Hour), 0, true)
	v = read(t, s, "weekly-only", u)
	if v.Target != "weekly" {
		t.Fatal(v)
	}
	u.FetchedAt = epoch.Add(10 * time.Minute)
	v = read(t, s, "weekly-only", u)
	if v.Weekly.State != Started {
		t.Fatal(v)
	}
	v, err := s.Observe("other-account", "codex", quota(epoch.Add(time.Minute), epoch.Add(5*time.Hour), 0, false))
	if err != nil || v.FiveHour.State != Unknown {
		t.Fatal(v, err)
	}
	if err := s.Invalidate("account", "codex"); err != nil {
		t.Fatal(err)
	}
	v = read(t, s, "codex", quota(epoch.Add(2*time.Minute), epoch.Add(5*time.Hour), 0, false))
	if v.FiveHour.State != Unknown {
		t.Fatal(v)
	}
}

func TestImmediatePreSendReadRetainsSlidingEvidence(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, d := range []time.Duration{0, time.Minute, time.Minute + time.Second} {
		now := epoch.Add(d)
		v := read(t, s, "codex", quota(now, now.Add(5*time.Hour), 0, false))
		if d >= time.Minute && v.FiveHour.State != NotStarted {
			t.Fatal(v)
		}
	}
	now := epoch.Add(time.Minute + time.Second)
	if _, err := s.Begin("account", "codex", true, now, now); err != nil {
		t.Fatal(err)
	}
}

func TestJitterCannotAccumulateIntoSlidingReset(t *testing.T) {
	w := &window{}
	reset := epoch.Add(5 * time.Hour)
	observe(w, Sample{At: epoch, Reset: reset, Seconds: 18000}, false)
	observe(w, Sample{At: epoch.Add(time.Minute), Reset: reset, Seconds: 18000}, false)
	for i := 1; i <= 10; i++ {
		v := observe(w, Sample{At: epoch.Add(time.Minute + time.Duration(i)*time.Second), Reset: reset.Add(time.Duration(i) * time.Second), Seconds: 18000}, false)
		if i > 5 && v.State == Started {
			t.Fatal("jitter accumulated beyond original anchor", v)
		}
	}
}

func TestBudgetAndCrashRecovery(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	now := epoch
	// Persistent transitions use fake time, including the backoff schedule.
	for i := 0; i < 4; i++ {
		read(t, s, "codex", quota(now, now.Add(5*time.Hour), 0, false))
		now = now.Add(time.Minute)
		u := quota(now, now.Add(5*time.Hour), 0, false)
		v := read(t, s, "codex", u)
		if v.NextEligible.After(now) {
			now = v.NextEligible
			// Fresh short-interval evidence after a long backoff.
			read(t, s, "codex", quota(now, now.Add(5*time.Hour), 0, false))
			now = now.Add(time.Minute)
			u = quota(now, now.Add(5*time.Hour), 0, false)
			read(t, s, "codex", u)
		}
		id, err := s.Begin("account", "codex", true, u.FetchedAt, now)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if _, err := s.Begin("account", "codex", false, time.Time{}, now); !errors.Is(err, ErrBusy) {
			t.Fatal(err)
		}
		if err := s.Finish("account", "codex", id, now, false); err != nil {
			t.Fatal(err)
		}
		read(t, s, "codex", quota(now, now.Add(5*time.Hour), 0, false))
	}
	now = now.Add(time.Minute)
	u := quota(now, now.Add(5*time.Hour), 0, false)
	v := read(t, Store{Dir: s.Dir}, "codex", u)
	if v.Recovery != "cooldown" {
		t.Fatal(v)
	}
	if _, err := s.Begin("account", "codex", true, u.FetchedAt, now); !errors.Is(err, ErrDeferred) {
		t.Fatal(err)
	}
	now = epoch.Add(2 * time.Hour)
	read(t, s, "codex", quota(now, now.Add(5*time.Hour), 0, false))
	now = now.Add(time.Minute)
	u = quota(now, now.Add(5*time.Hour), 0, false)
	read(t, s, "codex", u)
	_, err := s.Begin("account", "codex", true, now, now)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(claimDuration + time.Second)
	v = read(t, s, "codex", quota(now, now.Add(5*time.Hour), 0, false))
	if v.FiveHour.State != Unknown {
		t.Fatal("expired claim reused old evidence", v)
	}
}

func TestLockProcess(t *testing.T) {
	if path := os.Getenv("LIMITPING_TEST_LOCK"); path != "" {
		_, err := lock(path)
		if !errors.Is(err, ErrBusy) {
			os.Exit(2)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "test.lock")
	unlock, err := lock(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockProcess$")
	cmd.Env = append(os.Environ(), "LIMITPING_TEST_LOCK="+path)
	err = cmd.Run()
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	unlock, err = lock(path)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestLockReleasedAfterProcessCrash(t *testing.T) {
	if path := os.Getenv("LIMITPING_TEST_CRASH_LOCK"); path != "" {
		_, err := lock(path)
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(path+".ready", []byte("ready"), 0600); err != nil {
			os.Exit(3)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	path := filepath.Join(t.TempDir(), "crash.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockReleasedAfterProcessCrash$")
	cmd.Env = append(os.Environ(), "LIMITPING_TEST_CRASH_LOCK="+path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path + ".ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not acquire lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	unlock, err := lock(path)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestAutomaticBudgetsAreBucketSpecific(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.update("account", "codex", func(b *bucket) error { b.Attempts = []time.Time{epoch, epoch, epoch, epoch}; return nil }); err != nil {
		t.Fatal(err)
	}
	read(t, s, "spark:model", quota(epoch, epoch.Add(5*time.Hour), 0, false))
	now := epoch.Add(time.Minute)
	read(t, s, "spark:model", quota(now, now.Add(5*time.Hour), 0, false))
	if _, err := s.Begin("account", "spark:model", true, now, now); err != nil {
		t.Fatal("Codex budget blocked Spark", err)
	}
}

func TestResetBufferUsesPersistedPreviousBoundary(t *testing.T) {
	for _, weekly := range []bool{false, true} {
		for _, tc := range []struct {
			name          string
			buffer        time.Duration
			known, manual bool
			blocked       bool
		}{
			{"ten-minute-buffer", 10 * time.Minute, true, false, true},
			{"verification-covers-buffer", 35 * time.Second, true, false, false},
			{"unknown-reset", 10 * time.Minute, false, false, false},
			{"manual-bypass", 10 * time.Minute, true, true, false},
		} {
			t.Run(fmt.Sprintf("%s/weekly=%t", tc.name, weekly), func(t *testing.T) {
				dir := t.TempDir()
				s := Store{Dir: dir, ResetBuffer: tc.buffer}
				length := 5 * time.Hour
				if weekly {
					length = 7 * 24 * time.Hour
				}
				reset := epoch.Add(time.Minute)
				if tc.known {
					read(t, s, "codex", quota(epoch, reset, 1, weekly))
					// The expired snapshot itself must not erase the known boundary.
					read(t, s, "codex", quota(reset, reset, 0, weekly))
				}
				for _, d := range []time.Duration{time.Second, 61 * time.Second} {
					now := reset.Add(d)
					v := read(t, Store{Dir: dir}, "codex", quota(now, now.Add(length), 0, weekly))
					if tc.known && !v.PreviousReset.Equal(reset) {
						t.Fatalf("boundary lost on restart: %v", v.PreviousReset)
					}
					if !tc.known && !v.PreviousReset.IsZero() {
						t.Fatal("invented boundary")
					}
				}
				now := reset.Add(61 * time.Second)
				_, err := s.Begin("account", "codex", !tc.manual, now, now)
				if tc.blocked {
					if !errors.Is(err, ErrDeferred) {
						t.Fatalf("early send: %v", err)
					}
					// Repeated moving resets do not move the buffer deadline.
					for d := 2 * time.Minute; d <= 10*time.Minute; d += time.Minute {
						now = reset.Add(d)
						v := read(t, Store{Dir: dir}, "codex", quota(now, now.Add(length), 0, weekly))
						if !v.PreviousReset.Equal(reset) {
							t.Fatal(v.PreviousReset)
						}
					}
					if _, err := s.Begin("account", "codex", true, now, now); err != nil {
						t.Fatalf("buffer counted twice: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
