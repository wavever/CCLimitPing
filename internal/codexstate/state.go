// Package codexstate interprets Codex quota observations. It never sends requests.
package codexstate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wavever/CCLimitPing/internal/usage"
)

const (
	Started       = "started"
	NotStarted    = "not_started"
	Unknown       = "unknown"
	Interval      = time.Minute
	claimDuration = 3*time.Minute + 15*time.Second
	tolerance     = 5 * time.Second
)

var ErrBusy = errors.New("another ping or quota-state update is in progress.\nPlease try again in about a minute.")
var ErrDeferred = errors.New("automatic ping deferred; quota verification or cooldown is pending")

type Sample struct {
	At      time.Time
	Reset   time.Time
	Seconds int
	Used    float64
}

type window struct {
	Baseline Sample
	Latest   Sample
	State    string
	// ConfirmedReset is independent of the post-attempt failure baseline.
	ConfirmedReset time.Time
	PreviousReset  time.Time
}

type bucket struct {
	Plan       string
	FiveHour   window
	Weekly     window
	LastRead   time.Time
	AttemptEnd time.Time
	ClaimID    string
	ClaimUntil time.Time
	Attempts   []time.Time
	Retry      int
	LastAuto   time.Time
}

type diskState struct {
	Version int
	Buckets map[string]*bucket
}

type Store struct {
	Dir         string
	ResetBuffer time.Duration // automatic sends only; never shifts an observation
}

func sample(w usage.Window, now time.Time) Sample {
	return Sample{At: now, Reset: w.ResetsAt, Seconds: w.WindowSeconds, Used: w.UsedPercent}
}

func near(a, b time.Time) bool { return a.Sub(b).Abs() <= tolerance }
func valid(s Sample) bool {
	return s.Seconds > 0 && s.Used >= 0 && s.Used <= 100 && s.Reset.After(s.At) &&
		s.Reset.Sub(s.At) <= time.Duration(s.Seconds)*time.Second+tolerance
}

func observe(w *window, s Sample, duringAttempt bool) usage.StartStatus {
	previousReset := w.PreviousReset
	if w.Latest.Seconds != s.Seconds || s.At.Before(w.Latest.At) {
		previousReset = time.Time{}
	} else if !w.ConfirmedReset.IsZero() && !w.ConfirmedReset.After(s.At) {
		previousReset = w.ConfirmedReset
	}
	if !valid(s) {
		*w = window{PreviousReset: previousReset, Latest: s}
		return usage.StartStatus{State: Unknown}
	}
	old := w.Baseline
	if w.Latest.Seconds != s.Seconds || s.At.Before(w.Latest.At) ||
		(!w.ConfirmedReset.IsZero() && (!w.ConfirmedReset.After(s.At) || !near(w.ConfirmedReset, s.Reset))) {
		*w = window{PreviousReset: previousReset}
		old = Sample{}
	}
	state := Unknown
	if s.Used > 0 || (!w.ConfirmedReset.IsZero() && near(w.ConfirmedReset, s.Reset)) {
		state = Started
	} else if !duringAttempt && w.State == NotStarted && s.At.Sub(w.Latest.At) >= 0 && s.At.Sub(w.Latest.At) <= Interval &&
		near(s.Reset, s.At.Add(time.Duration(s.Seconds)*time.Second)) &&
		near(s.Reset, w.Latest.Reset.Add(s.At.Sub(w.Latest.At))) {
		// A fresh pre-send read may extend a recently established sliding pair.
		// It must not erase that evidence merely because it arrives within 60s.
		state = NotStarted
	} else if valid(old) && old.Seconds == s.Seconds && old.Reset.After(s.At) && s.At.Sub(old.At) >= Interval {
		if near(old.Reset, s.Reset) {
			state = Started
		} else if !duringAttempt && s.At.Sub(old.At) <= 10*time.Minute &&
			near(old.Reset, old.At.Add(time.Duration(old.Seconds)*time.Second)) &&
			near(s.Reset, s.At.Add(time.Duration(s.Seconds)*time.Second)) &&
			(s.Reset.Sub(old.Reset)-s.At.Sub(old.At)).Abs() <= tolerance {
			state = NotStarted
		}
	}
	if state == Started && w.ConfirmedReset.IsZero() {
		w.ConfirmedReset = s.Reset
	}
	// During a live claim do not collect evidence that could authorize its retry.
	if !duringAttempt && (old.At.IsZero() || state != Unknown || !old.Reset.After(s.At) ||
		s.At.Sub(old.At) > 10*time.Minute) {
		w.Baseline = s
	}
	w.Latest, w.State = s, state
	result := usage.StartStatus{State: state}
	if state == Unknown && !w.Baseline.At.IsZero() {
		result.DueAt = w.Baseline.At.Add(Interval)
		if !result.DueAt.After(s.At) {
			result.DueAt = s.At.Add(Interval)
		}
	}
	return result
}

func target(b *bucket) (string, *window) {
	if b.FiveHour.Latest.Seconds != 0 {
		return "five_hour", &b.FiveHour
	}
	if b.Weekly.Latest.Seconds != 0 {
		return "weekly", &b.Weekly
	}
	return "", nil
}

func prune(b *bucket, now time.Time) {
	kept := b.Attempts[:0]
	for _, at := range b.Attempts {
		if at.Add(time.Hour).After(now) {
			kept = append(kept, at)
		}
	}
	b.Attempts = kept
}

func view(b *bucket, now time.Time) *usage.Verification {
	v := &usage.Verification{FiveHour: status(b.FiveHour, now), Weekly: status(b.Weekly, now)}
	name, w := target(b)
	v.Target = name
	if w != nil {
		v.PreviousReset = w.PreviousReset
	}
	switch {
	case b.ClaimID != "" && b.ClaimUntil.After(now):
		v.Recovery, v.NextEligible = "ping_running", b.ClaimUntil
	case w == nil:
		v.Recovery = "unavailable"
	case w.State == Started:
		v.Recovery, v.NextEligible = "window_started", w.Latest.Reset
	default:
		v.Recovery = "verifying"
		v.NextEligible = now.Add(Interval)
		if w.State == NotStarted {
			v.Recovery, v.NextEligible = "ready", now
		}
		if d := retryDue(b); d.After(v.NextEligible) {
			v.Recovery, v.NextEligible = "backoff", d
		}
		if len(b.Attempts) >= 4 && b.Attempts[0].Add(time.Hour).After(v.NextEligible) {
			v.Recovery, v.NextEligible = "cooldown", b.Attempts[0].Add(time.Hour)
		}
	}
	return v
}

func status(w window, now time.Time) usage.StartStatus {
	s := usage.StartStatus{State: w.State}
	if s.State == "" {
		s.State = Unknown
	}
	if s.State == Unknown && !w.Baseline.At.IsZero() {
		s.DueAt = w.Baseline.At.Add(Interval)
		if !s.DueAt.After(now) {
			s.DueAt = now.Add(Interval)
		}
	}
	return s
}

func retryDue(b *bucket) time.Time {
	delay := time.Minute
	if b.Retry >= 2 {
		delay = 5 * time.Minute
	}
	if b.Retry >= 3 {
		delay = 15 * time.Minute
	}
	due := b.LastAuto.Add(delay)
	if b.AttemptEnd.Add(Interval).After(due) {
		due = b.AttemptEnd.Add(Interval)
	}
	return due
}

func (s Store) Observe(account, key string, u *usage.Usage) (*usage.Verification, error) {
	var v *usage.Verification
	err := s.update(account, key, func(b *bucket) error {
		now := u.FetchedAt
		prune(b, now)
		if b.Plan != u.Plan || now.Before(b.LastRead) {
			b.FiveHour, b.Weekly = window{}, window{}
		}
		b.Plan, b.LastRead = u.Plan, now
		if b.ClaimID != "" && !b.ClaimUntil.After(now) {
			// Crash recovery: first observation after an expired claim is a new baseline.
			clearEvidence(b)
			b.AttemptEnd, b.ClaimID = now, ""
		}
		live := b.ClaimID != ""
		observe(&b.FiveHour, sample(u.FiveHour, now), live)
		observe(&b.Weekly, sample(u.Weekly, now), live)
		_, w := target(b)
		if w != nil && w.State == Started {
			b.Retry, b.LastAuto = 0, time.Time{}
		}
		v = view(b, now)
		return nil
	})
	return v, err
}

func clearEvidence(b *bucket) {
	for _, w := range []*window{&b.FiveHour, &b.Weekly} {
		w.Baseline = Sample{}
		if w.ConfirmedReset.IsZero() {
			w.State = Unknown
		}
	}
}

// Begin is a short transaction, never a network-length lock. observedAt prevents
// an automatic sender from acting on a snapshot superseded by another process.
func (s Store) Begin(account, key string, automatic bool, observedAt, now time.Time) (string, error) {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	id := hex.EncodeToString(idBytes)
	err := s.update(account, key, func(b *bucket) error {
		prune(b, now)
		if b.ClaimID != "" && b.ClaimUntil.After(now) {
			return ErrBusy
		}
		if automatic {
			v := view(b, now)
			_, w := target(b)
			if b.ClaimID != "" || !b.LastRead.Equal(observedAt) || now.Sub(observedAt) > 3*time.Second ||
				w == nil || w.State != NotStarted || v.NextEligible.After(now) ||
				(!v.PreviousReset.IsZero() && v.PreviousReset.Add(s.ResetBuffer).After(now)) {
				return ErrDeferred
			}
			b.Attempts = append(b.Attempts, now)
			b.LastAuto = now
			if b.Retry < 3 {
				b.Retry++
			}
		}
		clearEvidence(b)
		b.ClaimID, b.ClaimUntil = id, now.Add(claimDuration)
		return nil
	})
	return id, err
}

func (s Store) Finish(account, key, id string, now time.Time, invalidate bool) error {
	return s.update(account, key, func(b *bucket) error {
		if id != b.ClaimID {
			return ErrBusy
		}
		clearEvidence(b)
		if invalidate {
			b.FiveHour, b.Weekly = window{}, window{}
		}
		b.AttemptEnd, b.ClaimID, b.ClaimUntil = now, "", time.Time{}
		return nil
	})
}

func (s Store) Invalidate(account, key string) error {
	return s.update(account, key, func(b *bucket) error { b.FiveHour, b.Weekly = window{}, window{}; return nil })
}

func (s Store) update(account, key string, fn func(*bucket) error) error {
	if account == "" || key == "" {
		return errors.New("quota account or bucket identity unavailable")
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(account))
	path := filepath.Join(s.Dir, hex.EncodeToString(hash[:])+".json")
	unlock, err := lock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	d := diskState{Version: 1, Buckets: map[string]*bucket{}}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &d); err != nil {
			return fmt.Errorf("invalid quota state: %w", err)
		}
		if d.Version != 1 || d.Buckets == nil {
			return errors.New("unsupported quota state")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	b := d.Buckets[key]
	if b == nil {
		b = &bucket{}
		d.Buckets[key] = b
	}
	if err := fn(b); err != nil {
		return err
	}
	data, err = json.Marshal(d)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".quota-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return replace(f.Name(), path)
}
