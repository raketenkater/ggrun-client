package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/raketenkater/ggrun/clientkit"
)

var version = "dev"

type connection struct {
	Version   int               `json:"version"`
	URL       string            `json:"url"`
	Token     string            `json:"token"`
	AllowHTTP bool              `json:"allow_http"`
	Profile   clientkit.Profile `json:"profile"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ggrun-client:", err)
		var exit *childExit
		if errors.As(err, &exit) && exit.code > 0 {
			os.Exit(exit.code)
		}
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "version", "--version":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	case "connect":
		return connect(args[1:])
	case "doctor":
		return doctor(args[1:])
	case "claude":
		return launch(args[1:])
	case "workflow-hook":
		return workflowHook(os.Stdin, os.Stdout)
	case "status":
		return status(args[1:])
	case "forget":
		return forget(args[1:])
	default:
		return fmt.Errorf("unknown command %q; use --help", args[0])
	}
}
func usage() {
	fmt.Println(`ggrun-client connects installed Claude Code to a ggrun server.

  ggrun-client connect URL [--name default] [--allow-http] [--token-stdin]
  ggrun-client doctor [--name default]
  ggrun-client claude [--connection default] [--resume [SESSION_ID]] [-- CLAUDE_ARGS...]
  ggrun-client forget [--name default]

Run claude from your laptop project directory. Files, tools and sessions stay
on the laptop. Server URLs work over HTTPS, LAN, SSH tunnels or optional VPNs.
Tokens are read privately, from GGRUN_AUTH_TOKEN, or through --token-stdin.
Remote cleartext HTTP requires --allow-http. Never put a token in the URL.`)
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,48}$`)

func configRoot() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ggrun-client"), nil
}
func connectionPath(name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("connection name must be 1–48 letters, numbers, underscores or hyphens")
	}
	root, err := configRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "profiles", name+".json"), nil
}
func load(name string) (connection, error) {
	var c connection
	path, err := connectionPath(name)
	if err != nil {
		return c, err
	}
	b, err := clientkit.ReadPrivate(path)
	if err != nil {
		return c, fmt.Errorf("read connection %q: %w; use connect first", name, err)
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("invalid saved connection")
	}
	if c.Version != 1 || len(c.Token) < 32 {
		return c, fmt.Errorf("unsupported or incomplete saved connection; reconnect")
	}
	c.URL, err = clientkit.BaseURL(c.URL, c.AllowHTTP)
	return c, err
}

// Diagnostic requests never follow redirects with a bearer credential.
func request(ctx context.Context, c connection, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.URL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	cl := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach gateway; check server, URL, DNS and network connection")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 401, 403:
		return fmt.Errorf("gateway rejected the credential; reconnect with the server's current gateway token")
	case 503:
		return fmt.Errorf("model is loading or recovering; wait, then retry this command")
	case 301, 302, 303, 307, 308:
		return fmt.Errorf("gateway redirected the request; connect directly to the final base URL (credentials were not forwarded)")
	default:
		return fmt.Errorf("gateway returned HTTP %d for %s; check the server and reverse-proxy path", resp.StatusCode, path)
	}
	if out == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("could not read gateway response")
	}
	if err = json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("gateway response is not valid JSON; check URL/proxy configuration")
	}
	return nil
}
func inspect(c connection) (clientkit.Profile, error) {
	var p clientkit.Profile
	if err := request(context.Background(), c, "/ggrun/client", &p); err != nil {
		return p, err
	}
	if err := p.Validate(); err != nil {
		return p, err
	}
	return p, request(context.Background(), c, "/health", nil)
}
func connect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	name := fs.String("name", "default", "connection name")
	allow := fs.Bool("allow-http", false, "allow cleartext HTTP to a remote address")
	stdin := fs.Bool("token-stdin", false, "read one token line from stdin")
	// Both connect URL --name NAME and connect --name NAME URL are accepted.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(append([]string{}, args[1:]...), args[0])
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ggrun-client connect URL [--name NAME] [--allow-http] [--token-stdin]")
	}
	path, err := connectionPath(*name)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		return fmt.Errorf("connection %q already exists; use forget --name %s before replacing it", *name, *name)
	}
	base, err := clientkit.BaseURL(fs.Arg(0), *allow)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(os.Getenv("GGRUN_AUTH_TOKEN"))
	if *stdin {
		line, err := bufio.NewReader(io.LimitReader(os.Stdin, 4097)).ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("read gateway token: %w", err)
		}
		token = strings.TrimSpace(line)
	} else if token == "" {
		if !term.IsTerminal(os.Stdin.Fd()) {
			return fmt.Errorf("use --token-stdin or GGRUN_AUTH_TOKEN for noninteractive connection setup")
		}
		fmt.Fprint(os.Stderr, "Gateway token (hidden): ")
		b, err := term.ReadPassword(os.Stdin.Fd())
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("read hidden token: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if len(token) < 32 || len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
		return fmt.Errorf("gateway token must be 32–4096 characters on one line")
	}
	c := connection{Version: 1, URL: base, Token: token, AllowHTTP: *allow}
	c.Profile, err = inspect(c)
	if err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err = clientkit.WritePrivate(path, b); err != nil {
		return fmt.Errorf("save private connection: %w", err)
	}
	fmt.Printf("Saved %s: %s, %d tokens per slot.\nRun ggrun-client doctor --name %s, then ggrun-client claude --connection %s in your project.\n", *name, c.Profile.ModelName, c.Profile.ContextPerSlot, *name, *name)
	return nil
}
func nameFlag(command string, args []string) (string, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	name := fs.String("name", "default", "connection name")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("unexpected argument")
	}
	return *name, nil
}
func forget(args []string) error {
	name, err := nameFlag("forget", args)
	if err != nil {
		return err
	}
	path, err := connectionPath(name)
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	fmt.Printf("Removed connection %s. Claude sessions remain on this machine.\n", name)
	return nil
}
func doctor(args []string) error {
	name, err := nameFlag("doctor", args)
	if err != nil {
		return err
	}
	c, err := load(name)
	if err != nil {
		return err
	}
	p, err := inspect(c)
	if err != nil {
		return err
	}
	fmt.Printf("Gateway ready. Protocol %d, server %s, %s, %d tokens/slot, %d slot(s), separate worker: %t.\n", p.ProtocolVersion, p.ServerVersion, p.ModelName, p.ContextPerSlot, p.Slots, p.SeparateWorker)
	if p.ModelID != c.Profile.ModelID {
		fmt.Println("Model identity has changed since connect; existing sessions require an exact model match to resume.")
	}
	_, v, err := claudeExecutable(false)
	if err != nil {
		return err
	}
	fmt.Println("Claude Code:", v)
	if err = transportConflict(); err != nil {
		return err
	}
	if err = settingsConflict(c, p, nil); err != nil {
		return err
	}
	fmt.Println("Connection checks passed. Tools run in the current laptop project. Count-tokens, Desktop and cloud sessions are not advertised.")
	return nil
}
