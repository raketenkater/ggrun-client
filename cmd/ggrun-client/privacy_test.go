package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func TestPrivacyEnvDefaultsAndUserOverride(t *testing.T) {
	env := privacyEnv([]string{"PATH=/x", "DISABLE_TELEMETRY=0"})
	got := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		got[k] = v
	}
	for _, kv := range privacyEnvDefaults {
		if kv[0] == "DISABLE_TELEMETRY" {
			continue
		}
		if got[kv[0]] != "1" {
			t.Fatalf("%s not set: %v", kv[0], env)
		}
	}
	if got["DISABLE_TELEMETRY"] != "0" {
		t.Fatalf("user value overridden: %v", env)
	}
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, "DISABLE_TELEMETRY=") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("duplicate DISABLE_TELEMETRY: %v", env)
	}
}

func TestDDGMCPOptIn(t *testing.T) {
	home(t)
	bin := t.TempDir()
	name := "uvx"
	if isWindows() {
		name = "uvx.exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GGRUN_CLAUDE_PERMISSION_MODE", "")
	args, err := clientArgsWith(nil, "default", profile(), "", privacyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "ddg") {
		t.Fatalf("ddg injected by default: %v", args)
	}
	args, err = clientArgsWith(nil, "default", profile(), "", privacyOptions{webSearchMCP: true})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := argValue(args, "--mcp-config"); !ok || v != ddgMCPConfig {
		t.Fatalf("opt-in lost ddg mcp: %v", args)
	}
	if v, _ := argValue(args, "--allowedTools"); !strings.Contains(v, "mcp__ddg-search__search") {
		t.Fatalf("opt-in lost allowedTools: %v", args)
	}
}

func TestWebFetchDisallowedUnlessOptIn(t *testing.T) {
	home(t)
	args, err := clientArgsWith(nil, "default", profile(), "", privacyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := argValue(args, "--disallowedTools"); !strings.Contains(v, "WebFetch") || !strings.Contains(v, "WebSearch") {
		t.Fatalf("WebFetch not denied: %v", args)
	}
	args, err = clientArgsWith(nil, "default", profile(), "", privacyOptions{allowWebFetch: true})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := argValue(args, "--disallowedTools"); strings.Contains(v, "WebFetch") {
		t.Fatalf("opt-in still denies WebFetch: %v", args)
	}
}

func TestPrivacyOptInFlagsAndEnv(t *testing.T) {
	o, err := launchOptionsFor([]string{"--web-search-mcp", "--allow-webfetch"})
	if err != nil || !o.webSearchMCP || !o.allowWebFetch {
		t.Fatalf("flags not parsed: %+v %v", o, err)
	}
	t.Setenv("GGRUN_CLIENT_ALLOW_WEBFETCH", "1")
	t.Setenv("GGRUN_CLIENT_WEB_SEARCH_MCP", "")
	if p := privacyOptionsFromEnv(); !p.allowWebFetch || p.webSearchMCP {
		t.Fatalf("env opt-ins wrong: %+v", p)
	}
}

func TestLaunchPrivacyCombinesEnvAndFlags(t *testing.T) {
	t.Setenv("GGRUN_CLIENT_WEB_SEARCH_MCP", "1")
	t.Setenv("GGRUN_CLIENT_ALLOW_WEBFETCH", "")
	o, err := launchOptionsFor(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := launchPrivacy(o); !p.webSearchMCP || p.allowWebFetch {
		t.Fatalf("env opt-in lost in the launch path: %+v", p)
	}
	o.allowWebFetch = true
	if p := launchPrivacy(o); !p.webSearchMCP || !p.allowWebFetch {
		t.Fatalf("flag opt-in lost in the launch path: %+v", p)
	}
}

func TestOwnWorkflowHookIsNotWarned(t *testing.T) {
	home(t)
	t.Chdir(t.TempDir())
	own := shellCommand(filepath.Join(t.TempDir(), "bin", "ggrun-client"), "workflow-hook")
	if !isOwnWorkflowHook(own) || isOwnWorkflowHook("other-tool workflow-hook") || isOwnWorkflowHook("ggrun-client review") {
		t.Fatalf("own-hook detection wrong for %q", own)
	}
	os.MkdirAll(".claude", 0o755)
	settings := `{"hooks":{"PreToolUse":[{"matcher":"Workflow","hooks":[{"type":"command","command":` + strconvQuote(own) + `}]}]}}`
	os.WriteFile(filepath.Join(".claude", "settings.json"), []byte(settings), 0o600)
	var b strings.Builder
	warnPreToolUseHooks(&b)
	if b.Len() != 0 {
		t.Fatalf("warned for the client's own workflow hook: %q", b.String())
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestPreToolUseHookWarning(t *testing.T) {
	home(t)
	proj := t.TempDir()
	t.Chdir(proj)
	var b strings.Builder
	warnPreToolUseHooks(&b)
	if b.Len() != 0 {
		t.Fatalf("warned without hooks: %q", b.String())
	}
	user := filepath.Join(os.Getenv("HOME"), ".claude")
	os.MkdirAll(user, 0o755)
	os.MkdirAll(".claude", 0o755)
	os.WriteFile(filepath.Join(user, "settings.json"), []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"SECRET=hunter2 run"}]}]}}`), 0o600)
	os.WriteFile(filepath.Join(".claude", "settings.local.json"), []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"x"}]}]}}`), 0o600)
	os.WriteFile(filepath.Join(".claude", "settings.json"), []byte(`{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"x"}]}]}}`), 0o600)
	warnPreToolUseHooks(&b)
	out := b.String()
	if !strings.Contains(out, filepath.Join(user, "settings.json")) || !strings.Contains(out, `matcher "Bash"`) ||
		!strings.Contains(out, "settings.local.json") || !strings.Contains(out, "bypass Auto-mode review") {
		t.Fatalf("missing warning: %q", out)
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, "in "+filepath.Join(".claude", "settings.json")+" ") {
		t.Fatalf("leaked command or warned for PostToolUse: %q", out)
	}
}
