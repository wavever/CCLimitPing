package update

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func useTempConfigDir(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestCompareOrdersByNumericComponent(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// String comparison gets this pair backwards, which is the whole point.
		{"0.10.0", "0.9.0", 1},
		{"0.9.0", "0.10.0", -1},
		{"1.0.0", "0.99.99", 1},
		{"0.9.0", "0.9.0", 0},
		{"v0.9.0", "0.9.0", 0},
		{"0.9.0-dev+abc123", "0.9.0", 0},
		{"0.9", "0.9.0", 0},
		{"0.9.1", "0.9", 1},
		// Garbage must read as "not newer" rather than prompting an upgrade.
		{"not-a-version", "0.9.0", -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAvailableRespectsDismissal(t *testing.T) {
	cases := []struct {
		name                       string
		current, latest, dismissed string
		want                       string
	}{
		{"newer release is offered", "0.9.0", "0.10.0", "", "0.10.0"},
		{"already current", "0.10.0", "0.10.0", "", ""},
		{"ahead of the release", "0.11.0", "0.10.0", "", ""},
		{"lookup produced nothing", "0.9.0", "", "", ""},
		{"dismissed exactly", "0.9.0", "0.10.0", "0.10.0", ""},
		// A dismissal covers that release only; the next one is announced.
		{"dismissed an older one", "0.9.0", "0.11.0", "0.10.0", "0.11.0"},
		{"local dev build of the same release", "0.10.0-dev+abc", "0.10.0", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Available(c.current, c.latest, c.dismissed); got != c.want {
				t.Fatalf("Available(%q, %q, %q) = %q, want %q", c.current, c.latest, c.dismissed, got, c.want)
			}
		})
	}
}

func TestLatestCachesAndRefreshes(t *testing.T) {
	useTempConfigDir(t)

	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v0.10.0"}`)),
			Request:    r,
		}, nil
	})}

	if got := Latest(context.Background(), client); got != "0.10.0" {
		t.Fatalf("Latest() = %q, want 0.10.0", got)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want one lookup", calls)
	}
	// Within the interval the cached value is served without touching the net.
	if got := Latest(context.Background(), client); got != "0.10.0" || calls != 1 {
		t.Fatalf("Latest() = %q after %d calls, want the cached value and no second lookup", got, calls)
	}

	s := Load()
	s.LastCheckedAt = time.Now().Add(-CheckInterval - time.Minute)
	if err := Save(s); err != nil {
		t.Fatal(err)
	}
	if got := Latest(context.Background(), client); got != "0.10.0" || calls != 2 {
		t.Fatalf("Latest() = %q after %d calls, want a refresh once stale", got, calls)
	}
}

// A version check must never turn into a command failure, and a dead endpoint
// must not be retried on every single invocation.
func TestLatestKeepsCachedValueWhenTheLookupFails(t *testing.T) {
	useTempConfigDir(t)
	if err := Save(State{LatestVersion: "0.10.0", LastCheckedAt: time.Now().Add(-CheckInterval - time.Minute)}); err != nil {
		t.Fatal(err)
	}

	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("nope"))}, nil
	})}

	if got := Latest(context.Background(), client); got != "0.10.0" {
		t.Fatalf("Latest() = %q, want the cached value preserved", got)
	}
	if Load().Stale(time.Now()) {
		t.Fatal("a failed lookup left the cache stale; it would retry on every command")
	}
	if got := Latest(context.Background(), client); got != "0.10.0" || calls != 1 {
		t.Fatalf("calls = %d, want the failure to hold off for the full interval", calls)
	}
}

// `upgrade` must see a release published after the cache was filled, however
// fresh that cache still is.
func TestRefreshIgnoresAFreshCache(t *testing.T) {
	useTempConfigDir(t)
	if err := Save(State{LatestVersion: "0.9.0", LastCheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"tag_name":"v0.10.0"}`)),
			Request:    r,
		}, nil
	})}
	if got := Refresh(context.Background(), client); got != "0.10.0" {
		t.Fatalf("Refresh() = %q, want the published 0.10.0 over the cached 0.9.0", got)
	}
	if got := Load().LatestVersion; got != "0.10.0" {
		t.Fatalf("cache = %q, want it updated so the notice agrees", got)
	}
}

// A failed lookup must not answer from the cache: that answer may predate the
// release, and `upgrade` would then refuse to install it.
func TestRefreshDoesNotFallBackToTheCache(t *testing.T) {
	useTempConfigDir(t)
	if err := Save(State{LatestVersion: "0.9.0", LastCheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("nope"))}, nil
	})}
	if got := Refresh(context.Background(), client); got != "" {
		t.Fatalf("Refresh() = %q on a failed lookup, want nothing", got)
	}
}

func TestDismissRecordsOnlyThatVersion(t *testing.T) {
	useTempConfigDir(t)
	if err := Save(State{LatestVersion: "0.10.0"}); err != nil {
		t.Fatal(err)
	}
	if err := Dismiss("0.10.0"); err != nil {
		t.Fatal(err)
	}
	s := Load()
	if s.DismissedVersion != "0.10.0" {
		t.Fatalf("DismissedVersion = %q", s.DismissedVersion)
	}
	if s.LatestVersion != "0.10.0" {
		t.Fatalf("Dismiss dropped the cached version: %+v", s)
	}
}

func TestLoadToleratesMissingAndCorruptState(t *testing.T) {
	useTempConfigDir(t)
	if got := Load(); got != (State{}) {
		t.Fatalf("Load() with no file = %+v, want the zero state", got)
	}
	path, err := statePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(State{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load(); got != (State{}) {
		t.Fatalf("Load() with corrupt file = %+v, want the zero state", got)
	}
}
