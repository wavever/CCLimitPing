package cli

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/wavever/CCLimitPing/internal/update"
)

func TestPromptUpdateOffersThreeChoices(t *testing.T) {
	setLocale(t, "C")
	var out bytes.Buffer
	promptUpdate(&out, localizedText(), strings.NewReader("2\n"), "0.10.0")

	got := out.String()
	for _, want := range []string{
		"Update available!",
		update.ReleaseNotesURL,
		"1. Update now",
		"2. Skip",
		"3. Skip until next version",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "-> 0.10.0") {
		t.Fatalf("prompt does not name the new version:\n%s", got)
	}
}

// Skipping must leave no trace, so the same release is offered again next time.
func TestPromptUpdateSkipDoesNotDismiss(t *testing.T) {
	setLocale(t, "C")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	for _, answer := range []string{"2\n", "\n", "nonsense\n", ""} {
		var out bytes.Buffer
		promptUpdate(&out, localizedText(), strings.NewReader(answer), "0.10.0")
		if got := update.Load().DismissedVersion; got != "" {
			t.Fatalf("answer %q dismissed %q, want nothing recorded", answer, got)
		}
	}
}

func TestPromptUpdateSkipUntilNextVersionRecordsTheDismissal(t *testing.T) {
	setLocale(t, "C")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var out bytes.Buffer
	promptUpdate(&out, localizedText(), strings.NewReader("3\n"), "0.10.0")

	if got := update.Load().DismissedVersion; got != "0.10.0" {
		t.Fatalf("DismissedVersion = %q, want 0.10.0", got)
	}
	// And that release is then silent, while the next one still speaks up.
	if got := update.Available(Version, "0.10.0", "0.10.0"); got != "" {
		t.Fatalf("Available after dismissal = %q, want silence", got)
	}
	if got := update.Available("0.9.0", "0.11.0", "0.10.0"); got != "0.11.0" {
		t.Fatalf("Available for the next release = %q, want 0.11.0", got)
	}
}

// The menu is driven the way the provider CLIs drive theirs — arrow keys and
// Enter — rather than by typing a number and pressing return.
func TestPromptUpdateMovesWithTheArrowKeys(t *testing.T) {
	setLocale(t, "C")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// The cursor starts on Skip, so one press of Down lands on the third
	// option, and Enter takes it.
	var out bytes.Buffer
	promptUpdate(&out, localizedText(), strings.NewReader("\x1b[B\r"), "0.10.0")
	if got := update.Load().DismissedVersion; got != "0.10.0" {
		t.Fatalf("down+Enter dismissed %q, want 0.10.0", got)
	}
	if got := out.String(); !strings.Contains(got, "Enter confirm") {
		t.Fatalf("prompt does not say which keys drive it:\n%s", got)
	}

	// Wrapping past the top reaches the same option from the other side, and
	// application-cursor mode (SS3) has to decode as well as CSI does.
	for _, keys := range []string{"\x1b[A\x1b[A\r", "\x1bOB\r"} {
		if err := update.Save(update.State{}); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		promptUpdate(&out, localizedText(), strings.NewReader(keys), "0.10.0")
		if got := update.Load().DismissedVersion; got != "0.10.0" {
			t.Fatalf("keys %q dismissed %q, want 0.10.0", keys, got)
		}
	}
}

// Whatever reads as "get out of my way" has to land on the option that changes
// nothing, since this notice interrupts a command the user actually asked for.
func TestPromptUpdateEscapeAndCtrlCSkip(t *testing.T) {
	setLocale(t, "C")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	for name, keys := range map[string]string{
		"esc":    "\x1b",
		"ctrl-c": "\x03",
		"ctrl-d": "\x04",
		// A stray Esc while the cursor sits elsewhere still means skip.
		"moved then esc": "\x1b[B\x1b",
	} {
		var out bytes.Buffer
		promptUpdate(&out, localizedText(), strings.NewReader(keys), "0.10.0")
		if got := update.Load().DismissedVersion; got != "" {
			t.Fatalf("%s dismissed %q, want nothing recorded", name, got)
		}
	}
}

func TestPromptUpdateIsLocalized(t *testing.T) {
	setLocale(t, "zh_CN.UTF-8")
	var out bytes.Buffer
	promptUpdate(&out, localizedText(), strings.NewReader("2\n"), "0.10.0")

	if got := out.String(); !strings.Contains(got, "有新版本") || !strings.Contains(got, "3. 跳过此版本") {
		t.Fatalf("prompt is not localized:\n%s", got)
	}
}

// After an upgrade the old binary must not go on to run the command: it is
// handed to the new binary, and where that cannot happen the run ends with a
// request to run it again.
func TestRerunUpgradedNeverContinuesOnTheOldBinary(t *testing.T) {
	setLocale(t, "C")
	exe, err := currentExecutable()
	if err != nil {
		t.Fatal(err)
	}
	var execPath string
	var execArgs []string
	exitCode := -1
	origExec, origExit, origArgs := execBinary, exitProcess, os.Args
	t.Cleanup(func() { execBinary, exitProcess, os.Args = origExec, origExit, origArgs })
	os.Args = []string{"/usr/local/bin/lmp", "status", "-v"}
	exitProcess = func(code int) { exitCode = code }

	// exec succeeding never returns, so the fake only records what it was
	// given; the fallback below is what runs when it does return.
	execBinary = func(path string, argv, _ []string) error {
		execPath, execArgs = path, argv
		return errors.New("exec unavailable")
	}
	var out bytes.Buffer
	rerunUpgraded(&out, localizedText(), "0.10.0")

	if execPath != exe {
		t.Fatalf("exec path = %q, want the installed binary %q", execPath, exe)
	}
	if strings.Join(execArgs, " ") != "/usr/local/bin/lmp status -v" {
		t.Fatalf("exec argv = %q, want the original invocation", execArgs)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want the run to end after a failed exec", exitCode)
	}
	if got := out.String(); !strings.Contains(got, "0.10.0") || !strings.Contains(got, "`lmp status -v`") {
		t.Fatalf("fallback does not ask to rerun the command:\n%s", got)
	}
}

// The root offers the notice ahead of every command a person runs, except the
// ones it would get in the way of.
func TestSkipsUpdateNotice(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"status"}, false},
		{[]string{"ping", "claude"}, false},
		{[]string{"watch"}, false},
		{[]string{"bg", "start"}, false},
		{[]string{"config", "show"}, false},
		{[]string{"hooks", "install"}, false},
		{[]string{"version"}, false},
		{[]string{"status", "--json"}, true},
		{[]string{"hook", "claude", "stop"}, true},
		{[]string{"upgrade"}, true},
		{[]string{"uninstall"}, true},
		{[]string{"help", "status"}, true},
		{[]string{"completion", "zsh"}, true},
		{[]string{"__complete", "st"}, true},
	}
	for _, c := range cases {
		root := newRootCmd()
		// Execute() attaches these; Find alone does not.
		root.InitDefaultHelpCmd()
		root.AddCommand(&cobra.Command{Use: cobra.ShellCompRequestCmd})
		cmd, rest, err := root.Find(c.args)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if err := cmd.ParseFlags(rest); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if got := skipsUpdateNotice(cmd); got != c.want {
			t.Errorf("skipsUpdateNotice(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

// The notice writes to stdout ahead of the command's own output, so anything
// that is not a person at a terminal must never see it.
func TestUpdateNoticeStaysSilentWithoutATerminal(t *testing.T) {
	setLocale(t, "C")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := update.Save(update.State{LatestVersion: "99.0.0"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	// os.Stdout under `go test` is a pipe, not a character device.
	updateNotice(t.Context(), &out, localizedText(), nil)
	if out.Len() != 0 {
		t.Fatalf("notice printed without a terminal:\n%s", out.String())
	}
}
