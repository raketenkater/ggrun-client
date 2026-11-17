package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/raketenkater/ggrun/clientkit"
)

// Replaceable in tests: hidden input, Claude install and the final launch.
var (
	readSecret = func(prompt string) (string, error) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	installClaude = func(version string) error {
		cmd := exec.Command("claude", "install", version)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	startClaude  = launch
	loadingRetry = 5 * time.Second
	loadingLimit = 30 * time.Minute
)

// guidedStart is the no-argument entry point: it reuses or creates a saved
// connection, repairs a changed token, waits for a loading model, checks the
// Claude Code version and launches with main-model controls.
func guidedStart(in *bufio.Reader, out io.Writer) error {
	ask := func(question, def string) string {
		if def != "" {
			fmt.Fprintf(out, "%s [%s]: ", question, def)
		} else {
			fmt.Fprintf(out, "%s: ", question)
		}
		line, _ := in.ReadString('\n')
		if line = strings.TrimSpace(line); line == "" {
			return def
		}
		return line
	}
	yes := func(question string) bool {
		a := strings.ToLower(ask(question+" (Y/n)", "Y"))
		return a == "y" || a == "yes"
	}

	names, err := savedConnections()
	if err != nil {
		return err
	}
	name := ""
	switch len(names) {
	case 0:
		fmt.Fprintln(out, "No saved ggrun server yet. Let's connect one.")
		if name, err = guidedConnect(ask, out, "default", ""); err != nil {
			return err
		}
	case 1:
		name = names[0]
	default:
		for i, n := range names {
			fmt.Fprintf(out, "  %d) %s\n", i+1, n)
		}
		pick := ask("Which server", "1")
		for i, n := range names {
			if pick == n || pick == fmt.Sprint(i+1) {
				name = n
			}
		}
		if name == "" {
			return fmt.Errorf("no saved connection %q", pick)
		}
	}

	c, err := load(name)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Checking %s (%s)...\n", name, c.URL)
	p, err := guidedReady(c, out)
	if errors.Is(err, errCredential) {
		fmt.Fprintln(out, "The server's gateway token changed. On the server run: ggrun gateway-token")
		token, terr := readSecret("New gateway token (hidden): ")
		if terr != nil {
			return terr
		}
		if c, err = updateToken(name, c, token); err != nil {
			return err
		}
		p, err = guidedReady(c, out)
	}
	if err != nil {
		if !errors.Is(err, errUnreachable) {
			return err
		}
		fmt.Fprintln(out, "Cannot reach", c.URL+": check that the server is running and this computer is on its network (VPN/Tailscale).")
		if !yes("Enter a different server URL") {
			return err
		}
		if err = os.Remove(mustConnectionPath(name)); err != nil {
			return err
		}
		if name, err = guidedConnect(ask, out, name, ""); err != nil {
			return err
		}
		if c, err = load(name); err != nil {
			return err
		}
		if p, err = guidedReady(c, out); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "Server ready: %s, %d tokens of context.\n", p.ModelName, p.ContextPerSlot)

	if _, v, verr := claudeExecutable(false); verr != nil || !claudeAtLeast(v, controlsClaudeVersion) {
		if _, lookErr := exec.LookPath("claude"); lookErr != nil {
			return fmt.Errorf("Claude Code is not installed; install it from https://code.claude.com/docs/en/setup, then run ggrun-client again")
		}
		fmt.Fprintf(out, "Claude Code %s is older than %s, which main-model review needs.\n", v, controlsClaudeVersion)
		if !yes("Update Claude Code to " + controlsClaudeVersion + " now") {
			return fmt.Errorf("Claude Code %s or newer is required; run: claude install %s", controlsClaudeVersion, controlsClaudeVersion)
		}
		if err = installClaude(controlsClaudeVersion); err != nil {
			return fmt.Errorf("claude install %s: %w", controlsClaudeVersion, err)
		}
	}

	args := []string{"--connection", name}
	answer := strings.ToLower(ask("Enable extra ggrun tool review? Native permissions remain active (y/N)", "N"))
	if answer == "y" || answer == "yes" {
		args = append(args, "--tool-review", "--control-timeout", "30m")
	}
	if path, perr := sessionPath(name); perr == nil {
		if _, rerr := resumedSession(path, "", p); rerr == nil {
			if yes("Resume your last session in this folder") {
				args = append(args, "--resume")
			}
		}
	}
	fmt.Fprintln(out, "Starting Claude Code. For native goals type /goal <condition>; extra ggrun tool review is independent.")
	return startClaude(args)
}

// guidedReady waits for a loading model instead of failing; other errors
// return immediately.
func guidedReady(c connection, out io.Writer) (clientkit.Profile, error) {
	deadline := time.Now().Add(loadingLimit)
	announced := false
	for {
		p, err := inspect(c)
		if !errors.Is(err, errLoading) {
			if announced {
				fmt.Fprintln(out)
			}
			return p, err
		}
		if time.Now().After(deadline) {
			return p, fmt.Errorf("server still loading after %s", loadingLimit)
		}
		if !announced {
			fmt.Fprint(out, "Server is loading the model; waiting (Ctrl+C to stop)")
			announced = true
		}
		fmt.Fprint(out, ".")
		time.Sleep(loadingRetry)
	}
}

func guidedConnect(ask func(string, string) string, out io.Writer, name, defURL string) (string, error) {
	raw := ask("Server URL (for example https://server.example.ts.net:8444)", defURL)
	base, err := clientkit.BaseURL(raw, false)
	if err != nil {
		return "", err
	}
	token, err := readSecret("Gateway token (hidden; on the server run: ggrun gateway-token): ")
	if err != nil {
		return "", err
	}
	if len(token) < 32 || len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
		return "", fmt.Errorf("gateway token must be 32–4096 characters on one line")
	}
	c := connection{Version: 1, URL: base, Token: token}
	if c.Profile, err = guidedReady(c, out); err != nil {
		return "", err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	if err = clientkit.WritePrivate(mustConnectionPath(name), b); err != nil {
		return "", fmt.Errorf("save private connection: %w", err)
	}
	fmt.Fprintf(out, "Saved server as %q.\n", name)
	return name, nil
}

func updateToken(name string, c connection, token string) (connection, error) {
	if len(token) < 32 || len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
		return c, fmt.Errorf("gateway token must be 32–4096 characters on one line")
	}
	c.Token = token
	p, err := guidedReady(c, io.Discard)
	if err != nil {
		return c, err
	}
	c.Profile = p
	b, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	return c, clientkit.ReplacePrivate(mustConnectionPath(name), b)
}

func savedConnections() ([]string, error) {
	root, err := configRoot()
	if err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(filepath.Join(root, "profiles", "*.json"))
	var names []string
	for _, f := range files {
		if n := strings.TrimSuffix(filepath.Base(f), ".json"); namePattern.MatchString(n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

func mustConnectionPath(name string) string {
	p, _ := connectionPath(name)
	return p
}
