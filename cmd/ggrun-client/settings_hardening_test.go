package main

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
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
