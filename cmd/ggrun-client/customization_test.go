package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomizationRoundTripAndExplicitPrecedence(t *testing.T) {
	home(t)
	p := profile()
	gw := fixture(t, &p)
	t.Setenv("GGRUN_AUTH_TOKEN", testToken)
	if err := connect([]string{gw.URL + "/prefix", "--name", "work"}); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(t.TempDir(), "plugin with spaces")
	os.Mkdir(plugin, 0700)
	mcp := filepath.Join(t.TempDir(), "mcp.json")
	os.WriteFile(mcp, []byte(`{"mcpServers":{}}`), 0600)
	c := customization{PluginDirs: []string{plugin, plugin}, MCPConfig: mcp, AppendSystemPrompt: "Follow the project brief.", PermissionMode: "auto"}
	b, _ := json.Marshal(c)
	file := filepath.Join(t.TempDir(), "custom.json")
	os.WriteFile(file, b, 0600)
	if err := importCustomization("work", file); err != nil {
		t.Fatal(err)
	}
	args, err := customizedArgs("work", []string{"--permission-mode=plan", "--append-system-prompt", "Explicit prompt"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "|")
	if strings.Count(joined, plugin) != 1 || strings.Contains(joined, "Follow the") || strings.Contains(joined, "--permission-mode|auto") || !strings.Contains(joined, mcp) {
		t.Fatalf("precedence: %v", args)
	}
	// Another connection must not acquire these launch choices.
	other, err := customizedArgs("other", nil)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-connection leak: %v %v", other, err)
	}
	// A removed local resource fails clearly instead of silently launching without it.
	os.Remove(mcp)
	if _, err := customizedArgs("work", nil); err == nil {
		t.Fatal("missing saved MCP config accepted")
	}
}

func TestCustomizationRejectsUnsupportedOrMalformedInput(t *testing.T) {
	for _, c := range []customization{{PermissionMode: "bypassPermissions"}, {PluginDirs: []string{"relative"}}, {MCPConfig: "relative"}} {
		if c.validate() == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	home(t)
	p := profile()
	gw := fixture(t, &p)
	t.Setenv("GGRUN_AUTH_TOKEN", testToken)
	if err := connect([]string{gw.URL + "/prefix"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "bad.json")
	for _, s := range []string{`{"unknown":true}`, `{} {}`, `{"permission_mode":"bypassPermissions"}`} {
		os.WriteFile(file, []byte(s), 0600)
		if err := importCustomization("default", file); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
