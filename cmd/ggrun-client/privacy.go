package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Privacy defaults. The client is used with sensitive data, so Claude Code's
// optional outbound traffic is off unless the user opts in or already set a
// value in their own environment.

const ddgMCPConfig = `{"mcpServers":{"ddg-search":{"command":"uvx","args":["duckduckgo-mcp-server"]}}}`

type privacyOptions struct {
	webSearchMCP  bool // --web-search-mcp / GGRUN_CLIENT_WEB_SEARCH_MCP=1
	allowWebFetch bool // --allow-webfetch / GGRUN_CLIENT_ALLOW_WEBFETCH=1
}

func privacyOptionsFromEnv() privacyOptions {
	return privacyOptions{
		webSearchMCP:  os.Getenv("GGRUN_CLIENT_WEB_SEARCH_MCP") == "1",
		allowWebFetch: os.Getenv("GGRUN_CLIENT_ALLOW_WEBFETCH") == "1",
	}
}

// launchPrivacy combines the env opt-ins with the launch flags: either opts in.
func launchPrivacy(o launchOptions) privacyOptions {
	p := privacyOptionsFromEnv()
	p.webSearchMCP = p.webSearchMCP || o.webSearchMCP
	p.allowWebFetch = p.allowWebFetch || o.allowWebFetch
	return p
}

// Variable names are from the official Claude Code docs,
// https://code.claude.com/docs/en/env-vars (DISABLE_TELEMETRY,
// DISABLE_ERROR_REPORTING, CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC,
// DISABLE_FEEDBACK_COMMAND, CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY).
var privacyEnvDefaults = [][2]string{
	{"DISABLE_TELEMETRY", "1"},
	{"DISABLE_ERROR_REPORTING", "1"},
	{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1"},
	{"DISABLE_FEEDBACK_COMMAND", "1"},
	{"CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY", "1"},
}

// privacyEnv adds the defaults to env, except for names already present
// (the user's own value wins).
func privacyEnv(env []string) []string {
	for _, kv := range privacyEnvDefaults {
		set := false
		for _, e := range env {
			if k, _, _ := strings.Cut(e, "="); envKeyEqual(k, kv[0]) {
				set = true
				break
			}
		}
		if !set {
			env = append(env, kv[0]+"="+kv[1])
		}
	}
	return env
}

func isWindows() bool { return runtime.GOOS == "windows" }

func envKeyEqual(a, b string) bool {
	if isWindows() {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// disallowedTools uses --disallowedTools (CLI reference,
// https://code.claude.com/docs/en/cli-reference).
func disallowedTools(p privacyOptions) []string {
	t := []string{"WebSearch"}
	if !p.allowWebFetch {
		t = append(t, "WebFetch")
	}
	return t
}

// preToolUseHookSources lists the settings files Claude Code loads for a launch
// (https://code.claude.com/docs/en/settings: user, shared project, project
// local). Managed settings are not readable here.
func preToolUseHookSources() []string {
	var files []string
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".claude", "settings.json"))
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		files = append(files, filepath.Join(d, "settings.json"))
	}
	return append(files, filepath.Join(".claude", "settings.json"), filepath.Join(".claude", "settings.local.json"))
}

// preToolUseHooks returns "path (matcher ...)" descriptions. Hook commands are
// never printed, since they may hold secrets; they are only compared with the
// client's own workflow hook, which can deny but never approves.
func preToolUseHooks(files []string) []string {
	var out []string
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m struct {
			Hooks struct {
				PreToolUse []struct {
					Matcher string `json:"matcher"`
					Hooks   []struct {
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"PreToolUse"`
			} `json:"hooks"`
		}
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		for _, h := range m.Hooks.PreToolUse {
			foreign := false
			for _, hook := range h.Hooks {
				if !isOwnWorkflowHook(hook.Command) {
					foreign = true
				}
			}
			if !foreign {
				continue
			}
			matcher := h.Matcher
			if matcher == "" {
				matcher = "*"
			}
			out = append(out, fmt.Sprintf("%s (matcher %q)", path, matcher))
		}
	}
	return out
}

// isOwnWorkflowHook reports whether command runs this client's workflow-hook
// subcommand (the README tells users with custom --settings to add it).
func isOwnWorkflowHook(command string) bool {
	// shellCommand quotes each part: 'exe' 'workflow-hook', with "& " on Windows.
	c := strings.TrimPrefix(strings.TrimSpace(command), "& ")
	c = strings.TrimSpace(strings.NewReplacer(`'`, "", `"`, "").Replace(c))
	exe, ok := strings.CutSuffix(c, " workflow-hook")
	if !ok {
		return false
	}
	base := strings.ToLower(filepath.Base(strings.TrimSpace(exe)))
	return strings.TrimSuffix(base, ".exe") == "ggrun-client"
}

func warnPreToolUseHooks(w io.Writer) {
	for _, h := range preToolUseHooks(preToolUseHookSources()) {
		fmt.Fprintf(w, "[ggrun-client] warning: PreToolUse hook in %s can pre-approve tool calls and bypass Auto-mode review\n", h)
	}
}
