package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

// Official Claude Code installer, per Anthropic's "Advanced setup" page
// (https://code.claude.com/docs/en/setup, "Install a specific version"):
//
//	macOS, Linux, WSL: curl -fsSL https://claude.ai/install.sh | bash -s <version>
//	Windows PowerShell: & ([scriptblock]::Create((irm https://claude.ai/install.ps1))) <version>
//
// The native installer needs no administrator rights and places the launcher
// in ~/.local/bin (claude.exe under %USERPROFILE%\.local\bin on Windows).
// This client never uses sudo.
const (
	claudeSetupDoc       = "https://code.claude.com/docs/en/setup"
	installClaudeEnv     = "GGRUN_CLIENT_INSTALL_CLAUDE"
	claudeInstallTimeout = 10 * time.Minute
)

// claudeInstallCommand returns the command line to show the user and the
// argv that runs it for goos. version pins the release the client qualifies.
func claudeInstallCommand(goos, version string) (shown string, argv []string) {
	if goos == "windows" {
		shown = "& ([scriptblock]::Create((irm https://claude.ai/install.ps1))) " + version
		return "powershell -Command \"" + shown + "\"", []string{"powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", shown}
	}
	shown = "curl -fsSL https://claude.ai/install.sh | bash -s " + version
	return shown, []string{"bash", "-o", "pipefail", "-c", shown}
}

// runClaudeInstaller runs the official installer, streaming its output. It is
// replaced in tests so nothing touches the network.
var runClaudeInstaller = func(goos, version string, out io.Writer) error {
	_, argv := claudeInstallCommand(goos, version)
	ctx, cancel := context.WithTimeout(context.Background(), claudeInstallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("installer timed out after %s", claudeInstallTimeout)
	}
	return err
}

func installClaudeOptIn() bool {
	v := strings.ToLower(os.Getenv(installClaudeEnv))
	return v == "1" || v == "true" || v == "yes"
}

// findClaude locates claude on PATH, or in the installer's ~/.local/bin that
// may not be on PATH yet for this process; in that case the directory is
// added to PATH so the later launch and version check find it too.
func findClaude() (string, bool) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	dir := filepath.Join(home, ".local", "bin")
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(dir, name)
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return "", false
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return p, true
}

// ensureClaude makes sure a claude executable exists, installing it with the
// official installer when it is missing. confirm asks the user (nil when
// there is no terminal); optIn skips the question. Without either it never
// installs: it names the exact command and stops.
func ensureClaude(out io.Writer, confirm func(string) bool, optIn bool) error {
	if _, ok := findClaude(); ok {
		return nil
	}
	shown, _ := claudeInstallCommand(runtime.GOOS, controlsClaudeVersion)
	manual := fmt.Sprintf("Claude Code is not installed. Install it from %s, for example:\n  %s", claudeSetupDoc, shown)
	if !optIn {
		if confirm == nil {
			return fmt.Errorf("%s\nthen run ggrun-client again (or set %s=1 / pass --install-claude to let ggrun-client run it)", manual, installClaudeEnv)
		}
		if !confirm(fmt.Sprintf("Claude Code is not installed. Install it now with Anthropic's official installer (version %s, from claude.ai, no sudo): %s", controlsClaudeVersion, shown)) {
			return errors.New(manual + "\nthen run ggrun-client again")
		}
	}
	fmt.Fprintf(out, "Installing Claude Code %s: %s\n", controlsClaudeVersion, shown)
	if err := runClaudeInstaller(runtime.GOOS, controlsClaudeVersion, out); err != nil {
		var ee *exec.ExitError
		status := err.Error()
		if errors.As(err, &ee) {
			status = fmt.Sprintf("exit status %d", ee.ExitCode())
		}
		return fmt.Errorf("the Claude Code installer failed (%s). Install it manually from %s:\n  %s", status, claudeSetupDoc, shown)
	}
	if _, ok := findClaude(); !ok {
		return fmt.Errorf("the installer finished but claude was not found on PATH or in ~/.local/bin; open a new terminal and run claude --version, or see %s", claudeSetupDoc)
	}
	fmt.Fprintln(out, "Claude Code installed.")
	return nil
}

// ensureClaudeForLaunch is the non-guided path: ask on a terminal, otherwise
// install only with the explicit opt-in.
func ensureClaudeForLaunch(optIn bool) error {
	var confirm func(string) bool
	if term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stderr.Fd()) {
		confirm = func(q string) bool {
			fmt.Fprint(os.Stderr, q+" (y/N) ")
			var a string
			fmt.Scanln(&a)
			a = strings.ToLower(strings.TrimSpace(a))
			return a == "y" || a == "yes"
		}
	}
	return ensureClaude(os.Stderr, confirm, optIn)
}
