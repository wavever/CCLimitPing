package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/wavever/CCLimitPing/internal/codexstate"
	"github.com/wavever/CCLimitPing/internal/provider"
	"github.com/wavever/CCLimitPing/internal/usage"
)

func startDescription(text cliText, s usage.StartStatus) string {
	switch s.State {
	case codexstate.Started:
		return text.verifyStarted
	case codexstate.NotStarted:
		return text.verifyNotStarted
	default:
		if !s.DueAt.IsZero() {
			d := time.Until(s.DueAt).Round(time.Second)
			if d < 0 {
				d = 0
			}
			return fmt.Sprintf("%s; recheck after %s", text.verifyUnknown, d)
		}
		return text.verifyUnknown
	}
}

func reportVerification(out io.Writer, text cliText, res *provider.TriggerResult) {
	if res == nil || res.Verification == nil {
		return
	}
	v := res.Verification
	s := v.FiveHour
	if v.Target == "weekly" {
		s = v.Weekly
	}
	description := startDescription(text, s)
	if pre := res.PreVerification; pre != nil && pre.Target == v.Target && s.State == codexstate.Started {
		before := pre.FiveHour
		if v.Target == "weekly" {
			before = pre.Weekly
		}
		if before.State == codexstate.Started {
			description = text.verifyAlreadyActive
		}
	}
	fmt.Fprintf(out, text.verifyQuotaStateFmt, v.Target, description)
	if v.Warning != "" {
		fmt.Fprintln(out, "  "+v.Warning)
	}
	if s.State == codexstate.Started {
		return
	}
	if !res.StatusEnabled {
		fmt.Fprintln(out, text.verifyDisabled)
		return
	}
	if s.DueAt.IsZero() {
		fmt.Fprintln(out, text.verifyNoBaseline)
		return
	}
	d := time.Until(s.DueAt).Round(time.Second)
	if d < 0 {
		d = 0
	}
	fmt.Fprintf(out, text.verifyCheckFmt, d)
}
