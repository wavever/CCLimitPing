// Package update tracks whether a newer limitping release exists, so a user is
// told about it instead of having to remember to check. The published version
// is cached in <config dir>/version.json and refreshed at most every
// CheckInterval, so the common case costs no network at all:
//
//	version.json  {latest_version, last_checked_at, dismissed_version}
//
// dismissed_version records a release the user chose to stop being told about;
// the next one after it is announced again.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wavever/CCLimitPing/internal/config"
)

const (
	// CheckInterval bounds how often the release endpoint is contacted. It
	// was a day, which hid a release for up to a day after it shipped; a few
	// hours keeps that short while staying far inside GitHub's anonymous rate
	// limit (60 an hour per address).
	CheckInterval = 4 * time.Hour
	// checkTimeout keeps a slow or unreachable endpoint from holding up a
	// command: an update notice is never worth making limitping feel stuck.
	checkTimeout = 2 * time.Second

	latestReleaseAPI = "https://api.github.com/repos/wavever/CCLimitPing/releases/latest"
	// ReleaseNotesURL is shown alongside the notice.
	ReleaseNotesURL = "https://github.com/wavever/CCLimitPing/releases/latest"
)

// State is the cached view of the newest published release.
type State struct {
	LatestVersion    string    `json:"latest_version"`
	LastCheckedAt    time.Time `json:"last_checked_at"`
	DismissedVersion string    `json:"dismissed_version"`
}

func statePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "version.json"), nil
}

// Load reads the cached state. A missing or unreadable file is not an error:
// it just means nothing is known yet.
func Load() State {
	path, err := statePath()
	if err != nil {
		return State{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}
	}
	return s
}

// Save writes the state, creating the config directory if needed.
func Save(s State) error {
	path, err := statePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Dismiss records that the user does not want to hear about version again.
func Dismiss(version string) error {
	s := Load()
	s.DismissedVersion = version
	return Save(s)
}

// Stale reports whether the cached version is old enough to re-check.
func (s State) Stale(now time.Time) bool {
	return now.Sub(s.LastCheckedAt) >= CheckInterval
}

// Latest returns the newest published version, refreshing the cache when it is
// stale. Any failure falls back to the cached value (possibly empty), because a
// version check must never turn into a command failure.
func Latest(ctx context.Context, client *http.Client) string {
	s := Load()
	if !s.Stale(time.Now()) {
		return s.LatestVersion
	}
	fetched, err := fetchLatest(ctx, client)
	// Record the attempt either way, so an endpoint that is down is retried
	// once per interval rather than on every single command.
	s.LastCheckedAt = time.Now()
	if err == nil && fetched != "" {
		s.LatestVersion = fetched
	}
	_ = Save(s)
	return s.LatestVersion
}

// Refresh asks the release endpoint whatever the cache holds, records what it
// found, and returns it; "" when the lookup fails. It is for a person asking
// outright — `upgrade` — where an answer cached before the release shipped
// would turn the request down as "already the latest". Unlike Latest it never
// falls back to the cache: a stale answer is the one thing it must not give.
func Refresh(ctx context.Context, client *http.Client) string {
	fetched, err := fetchLatest(ctx, client)
	if err != nil || fetched == "" {
		return ""
	}
	s := Load()
	s.LatestVersion = fetched
	s.LastCheckedAt = time.Now()
	_ = Save(s)
	return fetched
}

func fetchLatest(ctx context.Context, client *http.Client) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "limitping")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release lookup returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	return Normalize(r.TagName), nil
}

// Available returns the version worth telling the user about, or "" when they
// are current, the lookup produced nothing, or they dismissed this release.
// A dismissal only covers that exact version, so the next one is announced.
func Available(current, latest, dismissed string) string {
	if latest == "" {
		return ""
	}
	if Compare(latest, current) <= 0 {
		return ""
	}
	if dismissed != "" && Compare(latest, dismissed) <= 0 {
		return ""
	}
	return latest
}

// Normalize strips the leading v and any pre-release or build metadata, so a
// tag (v0.10.0), a release version (0.10.0) and a local build (0.10.0-dev+abc)
// all compare as the same release.
func Normalize(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	return v
}

// Compare orders two versions by numeric component: -1, 0 or 1. Non-numeric
// components sort as 0, so a malformed version degrades to "not newer" rather
// than producing a bogus upgrade prompt.
func Compare(a, b string) int {
	as := strings.Split(Normalize(a), ".")
	bs := strings.Split(Normalize(b), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		if d := component(as, i) - component(bs, i); d != 0 {
			if d < 0 {
				return -1
			}
			return 1
		}
	}
	return 0
}

func component(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(parts[i])
	if err != nil {
		return 0
	}
	return n
}
