package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// missingClaude gives an empty HOME and a PATH without claude, and fakes the
// installer: it records its arguments and, when create is set, drops a claude
// into ~/.local/bin like the real one (which is not on PATH).
func missingClaude(t *testing.T, create bool, fail error) *[]string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("PATH", t.TempDir())
	t.Setenv(installClaudeEnv, "")
	var calls []string
	old := runClaudeInstaller
	runClaudeInstaller = func(goos, version string, out io.Writer) error {
		calls = append(calls, goos+" "+version)
		io.WriteString(out, "fake installer output\n")
		if fail != nil {
			return fail
		}
		if create {
			dir := filepath.Join(h, ".local", "bin")
			os.MkdirAll(dir, 0o755)
			name := "claude"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			return os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755)
		}
		return nil
	}
	t.Cleanup(func() { runClaudeInstaller = old })
	return &calls
}

func TestInstallCommandPinsVersion(t *testing.T) {
	shown, argv := claudeInstallCommand("linux", "2.1.316")
	if shown != "curl -fsSL https://claude.ai/install.sh | bash -s 2.1.316" || argv[0] != "bash" || argv[len(argv)-1] != shown {
		t.Fatalf("%q %v", shown, argv)
	}
	shown, argv = claudeInstallCommand("windows", "2.1.316")
	if !strings.Contains(shown, "install.ps1") || !strings.HasSuffix(argv[len(argv)-1], ") 2.1.316") || argv[0] != "powershell.exe" {
		t.Fatalf("%q %v", shown, argv)
	}
	for _, g := range []string{"linux", "darwin", "windows"} {
		s, _ := claudeInstallCommand(g, "1.2.3")
		if strings.Contains(s, "sudo") {
			t.Fatal("sudo must never be used")
		}
	}
}

func TestEnsureClaudeInstallsOnYesAndRedetects(t *testing.T) {
	calls := missingClaude(t, true, nil)
	var asked string
	var out bytes.Buffer
	err := ensureClaude(&out, func(q string) bool { asked = q; return true }, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0], " "+controlsClaudeVersion) {
		t.Fatalf("installer calls: %v", *calls)
	}
	if !strings.Contains(asked, "claude.ai/install") || !strings.Contains(asked, "no sudo") {
		t.Fatalf("prompt does not name the command: %q", asked)
	}
	if !strings.Contains(out.String(), "fake installer output") {
		t.Fatal("installer output not streamed")
	}
	if _, ok := findClaude(); !ok {
		t.Fatal("claude not re-detected")
	}
	if p, err := exec.LookPath("claude"); err != nil || !strings.Contains(p, ".local") {
		t.Fatalf("~/.local/bin not added to PATH: %q %v", p, err)
	}
}

func TestEnsureClaudeDeclineStopsWithCommand(t *testing.T) {
	calls := missingClaude(t, true, nil)
	err := ensureClaude(io.Discard, func(string) bool { return false }, false)
	if err == nil || len(*calls) != 0 || !strings.Contains(err.Error(), claudeSetupDoc) || !strings.Contains(err.Error(), "install.") {
		t.Fatalf("%v %v", err, *calls)
	}
}

func TestEnsureClaudeNoTTYNeverInstalls(t *testing.T) {
	calls := missingClaude(t, true, nil)
	err := ensureClaude(io.Discard, nil, false)
	if err == nil || len(*calls) != 0 || !strings.Contains(err.Error(), installClaudeEnv) || !strings.Contains(err.Error(), "curl -fsSL") && !strings.Contains(err.Error(), "irm") {
		t.Fatalf("%v %v", err, *calls)
	}
}

func TestEnsureClaudeOptInInstallsWithoutPrompt(t *testing.T) {
	calls := missingClaude(t, true, nil)
	t.Setenv(installClaudeEnv, "1")
	if !installClaudeOptIn() {
		t.Fatal("env not honoured")
	}
	if err := ensureClaude(io.Discard, nil, installClaudeOptIn()); err != nil || len(*calls) != 1 {
		t.Fatalf("%v %v", err, *calls)
	}
}

func TestEnsureClaudeInstallerFailure(t *testing.T) {
	missingClaude(t, false, errors.New("boom"))
	err := ensureClaude(io.Discard, func(string) bool { return true }, false)
	if err == nil || !strings.Contains(err.Error(), "installer failed") || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), claudeSetupDoc) {
		t.Fatalf("%v", err)
	}
}

func TestEnsureClaudeInstallerSucceedsButNothingInstalled(t *testing.T) {
	missingClaude(t, false, nil)
	err := ensureClaude(io.Discard, nil, true)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("%v", err)
	}
}

func TestEnsureClaudeNoopWhenPresent(t *testing.T) {
	calls := missingClaude(t, false, nil)
	fakeClaude(t)
	if err := ensureClaude(io.Discard, nil, false); err != nil || len(*calls) != 0 {
		t.Fatalf("%v %v", err, *calls)
	}
}

func TestEnsureClaudeUsesInstalledButOffPathLauncher(t *testing.T) {
	calls := missingClaude(t, true, nil)
	runClaudeInstaller(runtime.GOOS, "x", io.Discard) // pre-existing install, not on PATH
	*calls = nil
	if err := ensureClaude(io.Discard, nil, false); err != nil || len(*calls) != 0 {
		t.Fatalf("%v %v", err, *calls)
	}
}

func TestLaunchOptionInstallClaude(t *testing.T) {
	o, err := launchOptionsFor([]string{"--install-claude"})
	if err != nil || !o.installClaude {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestGuidedInstallsMissingClaudeThenContinues(t *testing.T) {
	home(t)
	calls := missingClaude(t, false, nil)
	// The fake installer provides a claude that the client can query.
	dir := t.TempDir()
	exe, _ := os.Executable()
	b, _ := os.ReadFile(exe)
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	runClaudeInstaller = func(goos, version string, out io.Writer) error {
		*calls = append(*calls, version)
		t.Setenv("PATH", dir)
		t.Setenv("GGRUN_CLIENT_TEST_CHILD", "1")
		return os.WriteFile(filepath.Join(dir, name), b, 0o700)
	}
	s := gatewayFixture(t, testToken, 0)
	g := stubGuided(t, testToken)
	in := bufio.NewReader(strings.NewReader(s.URL + "\ny\ny\nn\n")) // url, install, update-if-asked, review
	var out bytes.Buffer
	if err := guidedStart(in, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(*calls) != 1 || len(g.launched) != 1 {
		t.Fatalf("calls %v launched %v\n%s", *calls, g.launched, out.String())
	}
}

// TestEnsureClaudeLiveInstaller runs the real official installer. It is
// skipped unless GGRUN_CLIENT_LIVE_INSTALL=1, and must run with HOME and the
// XDG dirs pointing at a disposable directory.
func TestEnsureClaudeLiveInstaller(t *testing.T) {
	if os.Getenv("GGRUN_CLIENT_LIVE_INSTALL") != "1" {
		t.Skip("set GGRUN_CLIENT_LIVE_INSTALL=1 with a disposable HOME")
	}
	if _, ok := findClaude(); ok {
		t.Fatal("claude already present; use a clean HOME and PATH")
	}
	var out bytes.Buffer
	if err := ensureClaude(&out, func(string) bool { return true }, false); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	p, v, err := claudeExecutable(false)
	if err != nil || v != controlsClaudeVersion {
		t.Fatalf("%q %q %v", p, v, err)
	}
	t.Logf("installed %s at %s", v, p)
}
