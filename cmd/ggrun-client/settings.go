package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/raketenkater/ggrun/clientkit"
)

func settingsFiles() []string {
	var files []string
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		files = append(files, filepath.Join(d, "settings.json"))
	} else if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".claude", "settings.json"))
	}
	files = append(files, filepath.Join(".claude", "settings.json"), filepath.Join(".claude", "settings.local.json"))
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("ProgramFiles"); d != "" {
			files = append(files, filepath.Join(d, "ClaudeCode", "managed-settings.json"))
		}
	case "darwin":
		files = append(files, "/Library/Application Support/ClaudeCode/managed-settings.json")
	default:
		files = append(files, "/etc/claude-code/managed-settings.json")
	}
	return files
}

// Claude applies env blocks after inheriting the child environment. Diagnose
// conflicting saved policies before a turn rather than mutating those files.
func settingsConflict(c connection, p clientkit.Profile, args []string) error {
	docs := map[string][]byte{}
	for _, path := range settingsFiles() {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot inspect Claude settings %s: %w", path, err)
		}
		docs[path] = b
	}
	for i, a := range args {
		value := ""
		if a == "--settings" && i+1 < len(args) {
			value = args[i+1]
		} else if strings.HasPrefix(a, "--settings=") {
			value = strings.TrimPrefix(a, "--settings=")
		}
		if value == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(value), "{") {
			docs["explicit --settings"] = []byte(value)
		} else {
			b, err := os.ReadFile(value)
			if err != nil {
				return fmt.Errorf("cannot inspect --settings file: %w", err)
			}
			docs[value] = b
		}
	}
	expected := map[string]string{}
	for _, pair := range p.Environment(c.URL, c.Token, os.Environ()) {
		k, v, _ := strings.Cut(pair, "=")
		expected[k] = v
	}
	for source, b := range docs {
		var document struct {
			Env map[string]string `json:"env"`
		}
		if json.Unmarshal(b, &document) != nil {
			return fmt.Errorf("cannot validate Claude settings JSON in %s", source)
		}
		for k, v := range document.Env {
			// Windows environment names are case-insensitive, including env
			// blocks Claude applies after inheriting our scoped environment.
			if runtime.GOOS == "windows" {
				k = strings.ToUpper(k)
			}
			if k == "CLAUDE_CODE_SUBAGENT_MODEL" && p.ClientAlias(v) {
				continue
			}
			for _, provider := range cloudProviders {
				if k == provider && v != "" && v != "0" && !strings.EqualFold(v, "false") {
					return fmt.Errorf("%s in %s selects a cloud transport; choose a ggrun-compatible Claude configuration for this session", k, source)
				}
			}
			if k == "ANTHROPIC_API_KEY" && v != "" {
				return fmt.Errorf("ANTHROPIC_API_KEY in %s overrides the gateway credential; remove that setting from the Claude configuration used for this connection", source)
			}
			if want, ok := expected[k]; ok && want != v {
				switch k {
				case "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_CUSTOM_HEADERS", "ANTHROPIC_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_SMALL_FAST_MODEL":
					return fmt.Errorf("%s in %s conflicts with the verified connection; adjust that setting in the Claude configuration used for this session", k, source)
				}
			}
		}
	}
	return nil
}
