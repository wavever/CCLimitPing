package cli

import (
	"strings"
	"testing"
	"time"
)

func TestScanBgPingHistory(t *testing.T) {
	since := time.Date(2026, 6, 14, 4, 0, 2, 500*1000*1000, time.Local)
	log := strings.Join([]string{
		"2026/06/14 03:59:59 [codex] ping sent, new window started",
		"not a limitping log line",
		"2026/06/14 04:00:01 [codex] window reset — triggering ping now…",
		"2026/06/14 04:00:02 [codex] ping sent, new window started — 12 tokens",
		"2026/06/14 04:00:03 [claude] ping failed: boom (retry in 30s)",
		"2026/06/14 04:00:04 [codex] DRY-RUN would ping now: codex exec",
		"2026/06/14 04:00:05 [claude] dry-run ping failed: boom (retry in 30s)",
	}, "\n")

	got, err := scanBgPingHistory(strings.NewReader(log), since)
	if err != nil {
		t.Fatalf("scanBgPingHistory: %v", err)
	}
	if got.total() != 4 || got.Succeeded != 1 || got.Failed != 2 || got.DryRun != 1 {
		t.Fatalf("history counts = total %d succeeded %d failed %d dry-run %d, want 4/1/2/1",
			got.total(), got.Succeeded, got.Failed, got.DryRun)
	}

	want := []struct {
		at       string
		provider string
		status   bgPingStatus
	}{
		{"2026-06-14 04:00:02", "codex", bgPingSucceeded},
		{"2026-06-14 04:00:03", "claude", bgPingFailed},
		{"2026-06-14 04:00:04", "codex", bgPingDryRun},
		{"2026-06-14 04:00:05", "claude", bgPingFailed},
	}
	if len(got.Attempts) != len(want) {
		t.Fatalf("attempts = %d, want %d", len(got.Attempts), len(want))
	}
	for i, w := range want {
		if got.Attempts[i].At.Format("2006-01-02 15:04:05") != w.at ||
			got.Attempts[i].Provider != w.provider ||
			got.Attempts[i].Status != w.status {
			t.Fatalf("attempt[%d] = %+v, want time %s provider %s status %s",
				i, got.Attempts[i], w.at, w.provider, w.status)
		}
	}
}
