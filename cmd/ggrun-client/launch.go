package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/raketenkater/ggrun/clientkit"
)

const targetClaudeVersion = "2.1.307"

var cloudProviders = []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_MANTLE"}

type childExit struct{ code int }

func (e *childExit) Error() string {
	return fmt.Sprintf("Claude Code exited with status %d; the server remains running", e.code)
}

func claudeExecutable(allowUntested bool) (string, string, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", "", fmt.Errorf("Claude Code is missing on this computer; install it from https://code.claude.com/docs/en/setup, then rerun doctor")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", "", fmt.Errorf("cannot read Claude Code version")
	}
	v := regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+\b`).FindString(string(b))
	if v != targetClaudeVersion && !allowUntested {
		return "", v, fmt.Errorf("Claude Code %s is outside this client's initial target %s; use --allow-untested-claude only after reviewing compatibility", v, targetClaudeVersion)
	}
	return path, v, nil
}

func transportConflict() error {
	for _, k := range cloudProviders {
		if v := strings.ToLower(os.Getenv(k)); v != "" && v != "0" && v != "false" {
			return fmt.Errorf("%s selects a cloud transport; unset it in this terminal to use your saved ggrun connection", k)
		}
	}
	for _, line := range strings.Split(os.Getenv("ANTHROPIC_CUSTOM_HEADERS"), "\n") {
		key, _, _ := strings.Cut(line, ":")
		if strings.EqualFold(strings.TrimSpace(key), "Authorization") || strings.EqualFold(strings.TrimSpace(key), "X-Api-Key") {
			return fmt.Errorf("ANTHROPIC_CUSTOM_HEADERS contains an authentication override; remove it in this terminal to use the saved gateway credential")
		}
	}
	return nil
}

type launchOptions struct {
	name, resume            string
	resuming, allowUntested bool
	args                    []string
}

func launchOptionsFor(args []string) (launchOptions, error) {
	o := launchOptions{name: "default"}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--":
			o.args = append([]string{}, args[i+1:]...)
			return o, nil
		case "--connection":
			i++
			if i == len(args) {
				return o, fmt.Errorf("--connection requires a name")
			}
			o.name = args[i]
		case "--allow-untested-claude":
			o.allowUntested = true
		case "--resume":
			o.resuming = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				o.resume = args[i]
			}
		default:
			return o, fmt.Errorf("unknown client option %q; pass Claude arguments after --", a)
		}
	}
	return o, nil
}

type session struct {
	ID, ModelID string
	Context     int
	Version     int
}

func sessionPath(name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("invalid connection name")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	sum := sha256.Sum256([]byte(cwd))
	root, err := configRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "sessions", name+"-"+hex.EncodeToString(sum[:16])+".json"), nil
}
func newSession(p clientkit.Profile) (session, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return session{}, err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b)
	return session{ID: h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], ModelID: p.ModelID, Context: p.ContextPerSlot, Version: 1}, nil
}
func resumedSession(path, id string, p clientkit.Profile) (session, error) {
	var s session
	b, err := clientkit.ReadPrivate(path)
	if err != nil {
		return s, fmt.Errorf("no verified session for this connection and project; start a new session first")
	}
	if json.Unmarshal(b, &s) != nil || s.Version != 1 || s.ID == "" {
		return s, fmt.Errorf("saved session metadata is invalid")
	}
	if id != "" && id != s.ID {
		return s, fmt.Errorf("this client only resumes the last verified session in this project (%s)", s.ID)
	}
	if s.ModelID != p.ModelID {
		return s, fmt.Errorf("server model/backend identity changed; start a new session to avoid silently routing the existing conversation to another model")
	}
	if p.ContextPerSlot < s.Context {
		return s, fmt.Errorf("served context decreased from %d to %d tokens; restore the previous capacity or start a new session", s.Context, p.ContextPerSlot)
	}
	s.Context = p.ContextPerSlot
	return s, nil
}
func saveSession(path string, s session) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// An existing session file is private and owned by this connector. Replacing
	// only this exact path never touches Claude's own transcripts or settings.
	return clientkit.ReplacePrivate(path, b)
}

func launch(args []string) error {
	o, err := launchOptionsFor(args)
	if err != nil {
		return err
	}
	if err = transportConflict(); err != nil {
		return err
	}
	c, err := load(o.name)
	if err != nil {
		return err
	}
	p, err := inspect(c)
	if err != nil {
		return err
	}
	if err = settingsConflict(c, p, o.args); err != nil {
		return err
	}
	path, v, err := claudeExecutable(o.allowUntested)
	if err != nil {
		return err
	}
	sessionFile, err := sessionPath(o.name)
	if err != nil {
		return err
	}
	var s session
	if o.resuming {
		s, err = resumedSession(sessionFile, o.resume, p)
	} else {
		s, err = newSession(p)
	}
	if err != nil {
		return err
	}
	// Prevent CLI overrides from invalidating model/context/session assumptions.
	for _, a := range o.args {
		k, _, _ := strings.Cut(a, "=")
		// Compact short options can hide resume/continue, such as -rID or
		// -vrc. Require their separate forms so the connector owns the session.
		if len(k) > 2 && strings.HasPrefix(k, "-") && !strings.HasPrefix(k, "--") && strings.ContainsAny(k[1:], "rc") {
			return fmt.Errorf("compact short option %q can override the verified session; use separate flags and the client's --resume option", k)
		}
		switch k {
		case "--model", "--fallback-model", "--session-id", "--resume", "-r", "--continue", "-c", "--fork-session":
			return fmt.Errorf("%s conflicts with the verified connection/session; use the client's --resume option", k)
		}
	}
	childArgs, err := clientArgs(o.args, o.name, p)
	if err != nil {
		return err
	}
	if o.resuming {
		childArgs = append([]string{"--resume", s.ID}, childArgs...)
		fmt.Fprintln(os.Stderr, "Resuming laptop session. The server may need to prefill again after a restart.")
	} else {
		childArgs = append([]string{"--session-id", s.ID}, childArgs...)
	}
	fmt.Fprintf(os.Stderr, "[ggrun-client] %s · %d tokens/slot · Claude %s · tools/files in current directory\n", p.ModelName, p.ContextPerSlot, v)
	cmd := exec.Command(path, childArgs...)
	cmd.Env = p.Environment(c.URL, c.Token, os.Environ())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	release := holdInterrupt()
	defer release()
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start Claude Code: %w", err)
	}
	if err = saveSession(sessionFile, s); err != nil {
		fmt.Fprintln(os.Stderr, "[ggrun-client] session metadata could not be saved; verified resume will be unavailable:", err)
	}
	err = cmd.Wait()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return &childExit{exit.ExitCode()}
		}
		return err
	}
	return nil
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}
func clientArgs(extra []string, name string, p clientkit.Profile) ([]string, error) {
	args := append([]string{}, extra...)
	if p.Policy.WorkflowHook {
		args = clientkit.WorkflowPromptArgs(args)
	}
	if !hasFlag(extra, "--permission-mode") {
		mode := os.Getenv("GGRUN_CLAUDE_PERMISSION_MODE")
		if mode == "" {
			mode = "default"
			if p.SeparateWorker {
				mode = "auto"
			} else {
				fmt.Fprintln(os.Stderr, "[ggrun-client] using normal permission prompts: no separate worker is advertised, and main-model Auto checks can exceed Claude's classifier deadline. Explicit --permission-mode choices are preserved.")
			}
		}
		if mode != "inherit" {
			args = append([]string{"--permission-mode", mode}, args...)
		}
	}
	// Existing custom settings remain authoritative. Supply only our narrow
	// hook/status additions and never write the user's Claude settings files.
	if !hasFlag(extra, "--settings") {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		hook := map[string]any{"type": "command", "command": shellCommand(exe, "workflow-hook"), "timeout": 5}
		if runtime.GOOS == "windows" {
			hook["shell"] = "powershell"
		}
		settings := map[string]any{}
		if p.Policy.WorkflowHook {
			settings["hooks"] = map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Workflow", "hooks": []any{hook}}}}
		}
		if !customStatusLine() && os.Getenv("GGRUN_CLAUDE_PROGRESS") != "0" {
			// Status-line commands use the platform's normal shell.
			statusCommand := shellCommand(exe, "status", "--name", name)
			if runtime.GOOS == "windows" {
				statusCommand = `"` + exe + `" status --name ` + name
			}
			settings["statusLine"] = map[string]any{"type": "command", "command": statusCommand, "refreshInterval": 2, "padding": 0}
		}
		b, err := json.Marshal(settings)
		if err != nil {
			return nil, err
		}
		args = append([]string{"--settings", string(b)}, args...)
	} else {
		fmt.Fprintln(os.Stderr, "[ggrun-client] custom --settings supplied; keep the Workflow timeout hook configured for long local-model workflows")
	}
	if !hasFlag(extra, "--disallowedTools") {
		args = append([]string{"--disallowedTools", "WebSearch"}, args...)
	}
	if !hasFlag(extra, "--mcp-config") {
		if _, err := exec.LookPath("uvx"); err == nil {
			args = append([]string{"--mcp-config", `{"mcpServers":{"ddg-search":{"command":"uvx","args":["duckduckgo-mcp-server"]}}}`}, args...)
			if !hasFlag(extra, "--allowedTools") && !hasFlag(extra, "--allowed-tools") {
				args = append([]string{"--allowedTools", "mcp__ddg-search__search,mcp__ddg-search__fetch_content"}, args...)
			}
		} else {
			fmt.Fprintln(os.Stderr, "[ggrun-client] add a search MCP or install uvx on this laptop for web research")
		}
	}
	return args, nil
}
func shellCommand(parts ...string) string {
	out := make([]string, len(parts))
	for i, s := range parts {
		if runtime.GOOS == "windows" {
			out[i] = "'" + strings.ReplaceAll(s, "'", "''") + "'"
		} else {
			out[i] = "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
		}
	}
	prefix := ""
	if runtime.GOOS == "windows" {
		prefix = "& "
	}
	return prefix + strings.Join(out, " ")
}
func customStatusLine() bool {
	var files []string
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".claude", "settings.json"))
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		files = append(files, filepath.Join(d, "settings.json"))
	}
	files = append(files, filepath.Join(".claude", "settings.json"), filepath.Join(".claude", "settings.local.json"))
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(b, &m) == nil {
			if v, ok := m["statusLine"]; ok && string(v) != "null" {
				return true
			}
		}
	}
	return false
}
func workflowHook(in io.Reader, out io.Writer) error {
	var input struct {
		ToolName   string                 `json:"tool_name"`
		ToolInput  map[string]interface{} `json:"tool_input"`
		Transcript string                 `json:"transcript_path"`
	}
	if err := json.NewDecoder(io.LimitReader(in, 4<<20)).Decode(&input); err != nil {
		return fmt.Errorf("invalid Workflow hook input")
	}
	if input.ToolName != "Workflow" || input.ToolInput == nil {
		_, err := io.WriteString(out, "{}\n")
		return err
	}
	result := map[string]any{"hookEventName": "PreToolUse"}
	if err := clientkit.PatchWorkflowInput(input.ToolInput, input.Transcript); err != nil {
		result["permissionDecision"] = "deny"
		result["permissionDecisionReason"] = fmt.Sprintf("Cannot apply the local-model timeout policy: %v; retry with script or scriptPath", err)
	} else {
		// Updating the script never grants permission; normal user/reviewer policy
		// must still decide whether the Workflow tool may execute.
		result["updatedInput"] = input.ToolInput
	}
	return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": result})
}
func status(args []string) error {
	// Best-effort status: bounded, authenticated, never runs a model probe.
	name, err := nameFlag("status", args)
	if err != nil {
		return err
	}
	c, err := load(name)
	if err != nil {
		fmt.Println("ggrun: reconnect needed")
		return nil
	}
	var s struct{ Active, Queued, Limit int }
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err = request(ctx, c, "/ggrun/router", &s); err != nil {
		fmt.Println("ggrun: connection unavailable")
		return nil
	}
	fmt.Printf("ggrun · active %d/%d · queued %d\n", s.Active, s.Limit, s.Queued)
	return nil
}
