package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
func settingsDocuments(args []string) (map[string][]byte, error) {
	docs := map[string][]byte{}
	for _, path := range settingsFiles() {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("cannot inspect Claude settings %s: %w", path, err)
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
				return nil, fmt.Errorf("cannot inspect --settings file: %w", err)
			}
			docs[value] = b
		}
	}
	return docs, nil
}

func settingsConflict(c connection, p clientkit.Profile, args []string) error {
	docs, err := settingsDocuments(args)
	if err != nil {
		return err
	}
	expected := map[string]string{}
	for _, pair := range p.Environment(c.URL, c.Token, os.Environ()) {
		k, v, _ := strings.Cut(pair, "=")
		expected[k] = v
	}
	limit, err := settingsWorkflowCapacity(p, docs, expected["CLAUDE_CODE_WORKFLOW_MAX_CONCURRENT_AGENTS"])
	if err != nil {
		return err
	}
	expected["CLAUDE_CODE_WORKFLOW_MAX_CONCURRENT_AGENTS"] = strconv.Itoa(limit)
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
			if k == "CLAUDE_CODE_WORKFLOW_MAX_CONCURRENT_AGENTS" {
				limit, _ := strconv.Atoi(expected[k])
				n, err := strconv.Atoi(v)
				if err == nil && n >= 1 && n <= limit {
					continue
				}
				return fmt.Errorf("%s in %s exceeds the verified executor capacity; use a positive limit at most %d", k, source, limit)
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

// Settings env blocks are applied by Claude after the inherited environment.
// Conservatively account for a utility subagent selection in any settings
// source so it cannot inherit a larger main-route Workflow cap.
func settingsWorkflowCapacity(p clientkit.Profile, docs map[string][]byte, inherited string) (int, error) {
	limit, err := strconv.Atoi(inherited)
	if err != nil || limit < 1 {
		limit = p.Slots
	}
	for source, b := range docs {
		var doc struct {
			Env map[string]string `json:"env"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return 0, fmt.Errorf("cannot validate Claude settings JSON in %s", source)
		}
		for k, v := range doc.Env {
			if runtime.GOOS == "windows" {
				k = strings.ToUpper(k)
			}
			if k == "CLAUDE_CODE_SUBAGENT_MODEL" && (v == p.UtilityAlias || v == "haiku") {
				n := 1
				if p.UtilityRoute != nil {
					n = p.UtilityRoute.Slots
				}
				limit = min(limit, n)
			}
		}
	}
	return limit, nil
}

func applyWorkflowSettingsCapacity(env []string, p clientkit.Profile, args []string) ([]string, error) {
	docs, err := settingsDocuments(args)
	if err != nil {
		return nil, err
	}
	for i, pair := range env {
		if k, v, ok := strings.Cut(pair, "="); ok && k == "CLAUDE_CODE_WORKFLOW_MAX_CONCURRENT_AGENTS" {
			n, err := settingsWorkflowCapacity(p, docs, v)
			if err != nil {
				return nil, err
			}
			env[i] = k + "=" + strconv.Itoa(n)
		}
	}
	return env, nil
}
