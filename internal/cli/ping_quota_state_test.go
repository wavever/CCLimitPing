package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

func TestPingAlreadyActiveBeforePing(t *testing.T) {
	for _, target := range []string{"weekly", "five_hour"} {
		for _, before := range []string{"", "unknown", "not_started", "started"} {
			for _, after := range []string{"unknown", "not_started", "started"} {
				for _, failed := range []bool{false, true} {
					res := &provider.TriggerResult{StatusEnabled: true,
						Verification: &usage.Verification{Target: target,
							FiveHour: usage.StartStatus{State: after}, Weekly: usage.StartStatus{State: after}},
					}
					if before != "" {
						res.PreVerification = &usage.Verification{Target: target,
							FiveHour: usage.StartStatus{State: before}, Weekly: usage.StartStatus{State: before}}
					}
					var err error
					if failed {
						err = fmt.Errorf("CLI failed")
					}
					for _, text := range []cliText{enText, zhText} {
						var out bytes.Buffer
						report(&out, text, "codex", time.Now(), res, err)
						want := before == "started" && after == "started"
						if strings.Contains(out.String(), text.verifyAlreadyActive) != want {
							t.Fatalf("%s/%s/%s/failed=%v: %s", target, before, after, failed, out.String())
						}
						if want && !strings.Contains(out.String(), fmt.Sprintf(text.verifyQuotaStateFmt, target, text.verifyAlreadyActive)) {
							t.Fatal(out.String())
						}
					}
				}
			}
		}
	}
}
