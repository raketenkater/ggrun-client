package main

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/raketenkater/ggrun/clientkit"
)

func TestEmptySavedConnectionSettingsCannotClearVerifiedRoute(t *testing.T) {
	home(t)
	c := connection{URL: "https://models.example", Token: testToken}
	for _, key := range []string{
		"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL",
		"ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_MAX_CONTEXT_TOKENS",
		"CLAUDE_CODE_SUBAGENT_MODEL",
	} {
		t.Run(key, func(t *testing.T) {
			settings, _ := json.Marshal(map[string]any{"env": map[string]string{key: ""}})
			err := settingsConflict(c, profile(), []string{"--settings", string(settings)})
			if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), testToken) {
				t.Fatalf("empty %s escaped route validation or exposed credentials: %v", key, err)
			}
		})
	}
	// Clearing a cloud credential/provider or an unrelated setting is safe.
	err := settingsConflict(c, profile(), []string{"--settings", `{"env":{"ANTHROPIC_API_KEY":"","CLAUDE_CODE_USE_BEDROCK":"","MY_SETTING":""}}`})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowSettingsCannotOvercommitUtilityExecutor(t *testing.T) {
	home(t)
	p := profile()
	p.Slots = 4
	p.UtilityRoute = &clientkit.UtilityRoute{ContextPerSlot: 8192, Slots: 1, SeparateBackend: true, FallbackToMain: true}
	c := connection{URL: "http://127.0.0.1", Profile: p}
	args := []string{"--settings", `{"env":{"CLAUDE_CODE_SUBAGENT_MODEL":"local-fast"}}`}
	if err := settingsConflict(c, p, args); err != nil {
		t.Fatal(err)
	}
	env, err := applyWorkflowSettingsCapacity(p.Environment(c.URL, "", nil), p, args)
	if err != nil || !strings.Contains(strings.Join(env, "\n"), "CLAUDE_CODE_WORKFLOW_MAX_CONCURRENT_AGENTS=1\n") {
		t.Fatal("utility executor inherited main capacity", err)
	}
	for _, cap := range []string{"2", "0", "bad"} {
		b, _ := json.Marshal(map[string]any{"env": map[string]string{"CLAUDE_CODE_SUBAGENT_MODEL": "local-fast", "CLAUDE_CODE_WORKFLOW_MAX_CONCURRENT_AGENTS": cap}})
		if err := settingsConflict(c, p, []string{"--settings", string(b)}); err == nil {
			t.Fatalf("accepted invalid utility cap %q", cap)
		}
	}
}

func TestWindowsSavedSettingsCannotBypassRouteChecksByCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows environment names are case-insensitive")
	}
	home(t)
	c := connection{URL: "https://models.example", Token: testToken}
	for _, env := range []map[string]string{
		{"anthropic_base_url": "https://another.example"},
		{"Anthropic_Base_Url": ""},
		{"anthropic_auth_token": "private-old-token"},
		{"claude_code_use_bedrock": "1"},
	} {
		settings, _ := json.Marshal(map[string]any{"env": env})
		err := settingsConflict(c, profile(), []string{"--settings", string(settings)})
		if err == nil || strings.Contains(err.Error(), "private-old-token") || strings.Contains(err.Error(), testToken) {
			t.Fatalf("case bypass or credential exposure: %v", err)
		}
	}
}
