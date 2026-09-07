package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/usage"
)

func TestStatusRecoveryGuidance(t *testing.T) {
	for _, text := range []cliText{enText, zhText} {
		for _, tc := range []struct {
			state string
			want  string
		}{
			{"window_started", ""},
			{"verifying", text.verifyStatusCheck},
			{"backoff", fmtClock(text, time.Date(2026, 9, 7, 12, 4, 0, 0, time.UTC))},
			{"cooldown", fmtClock(text, time.Date(2026, 9, 7, 12, 4, 0, 0, time.UTC))},
			{"ping_running", text.verifyRunning},
			{"ready", text.verifyReady},
			{"unavailable", text.verifyUnavailable},
		} {
			t.Run(tc.state+"/"+text.verifyStarted, func(t *testing.T) {
				u := &usage.Usage{Provider: "codex"}
				var plain bytes.Buffer
				printUsage(&plain, text, u, false, "")
				u.Verification = &usage.Verification{
					Recovery:     tc.state,
					NextEligible: time.Date(2026, 9, 7, 12, 4, 0, 0, time.UTC),
				}
				var out bytes.Buffer
				printUsage(&out, text, u, false, "")
				if tc.want == "" {
					if out.String() != plain.String() {
						t.Fatal(out.String())
					}
				} else if !strings.Contains(out.String(), tc.want) {
					t.Fatal(out.String())
				}
				u.Verification.Warning = "observation warning"
				out.Reset()
				printUsage(&out, text, u, false, "")
				if !strings.Contains(out.String(), "observation warning") {
					t.Fatal(out.String())
				}
			})
		}
	}
}
