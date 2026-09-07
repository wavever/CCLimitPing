package scheduler

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/wavever/CCLimitPing/internal/provider"
)

func TestCodexStartupHintLog(t *testing.T) {
	for _, home := range []string{"", "/tmp/codex home"} {
		t.Run(home, func(t *testing.T) {
			t.Setenv("CODEX_HOME", home)
			var out bytes.Buffer
			s := &Scheduler{log: log.New(&out, "", 0)}
			res := &provider.TriggerResult{Command: "codex -C /tmp/ping-repo ok"}
			err := fmt.Errorf("wrapped: %w", &provider.CodexCompletionError{Reason: "timeout"})
			s.logCodexStartupHint("spark", res, err)
			got := out.String()
			if !strings.Contains(got, res.Command) || !strings.Contains(got, "confirmation dialogs") ||
				!strings.Contains(got, "CODEX_HOME") || !strings.Contains(got, "[spark]") {
				t.Fatal(got)
			}
			if home != "" && !strings.Contains(got, fmt.Sprintf("%q", home)) {
				t.Fatal(got)
			}
			if home == "" && !strings.Contains(got, "unset") {
				t.Fatal(got)
			}
			out.Reset()
			s.logCodexStartupHint("codex", res, errors.New("process failed"))
			s.logCodexStartupHint("codex", res, &provider.UsageHTTPError{StatusCode: 403})
			s.logCodexStartupHint("codex", nil, err)
			if out.Len() != 0 {
				t.Fatal(out.String())
			}
		})
	}
}
