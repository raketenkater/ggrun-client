package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// A long general API timeout does not extend native Claude's classifier
// deadline. Auto remains the product default even without a companion; the
// reviewer route must be qualified independently, never replaced by approvals.
func TestPermissionDefaultsToAutoWithAndWithoutWorker(t *testing.T) {
	home(t)
	t.Setenv("GGRUN_CLAUDE_PERMISSION_MODE", "")
	for _, worker := range []bool{false, true} {
		p := profile()
		p.SeparateWorker = worker
		args, err := clientArgs([]string{"--settings", "{}", "--mcp-config", "{}"}, "default", p, "")
		if err != nil {
			t.Fatal(err)
		}
		want := "auto"
		joined := "\n" + strings.Join(args, "\n") + "\n"
		if !strings.Contains(joined, "\n--permission-mode\n"+want+"\n") {
			t.Fatalf("separate worker %t: wanted %q, got %v", worker, want, args)
		}
	}
}

func TestPermissionOverridesRemainAuthoritativeWithoutWorker(t *testing.T) {
	home(t)
	for _, tc := range []struct {
		name, env, want string
		extra           []string
	}{
		{"explicit manual", "auto", "default", []string{"--permission-mode", "default"}},
		{"explicit auto", "", "auto", []string{"--permission-mode", "auto"}},
		{"inline explicit", "auto", "plan", []string{"--permission-mode=plan"}},
		{"environment", "auto", "auto", nil},
		{"inherit", "inherit", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GGRUN_CLAUDE_PERMISSION_MODE", tc.env)
			extra := append([]string{"--settings", "{}", "--mcp-config", "{}"}, tc.extra...)
			args, err := clientArgs(extra, "default", profile(), "")
			if err != nil {
				t.Fatal(err)
			}
			var modes []string
			for i, arg := range args {
				if arg == "--permission-mode" && i+1 < len(args) {
					modes = append(modes, args[i+1])
				} else if strings.HasPrefix(arg, "--permission-mode=") {
					modes = append(modes, strings.TrimPrefix(arg, "--permission-mode="))
				}
			}
			if tc.want == "" {
				if len(modes) != 0 {
					t.Fatalf("inherit injected a permission override: %v", modes)
				}
			} else if len(modes) != 1 || modes[0] != tc.want {
				t.Fatalf("want exactly %q, got %v", tc.want, modes)
			}
		})
	}
}

// Claude's "not eligible with this gateway" Auto-mode notice is suppressed by
// CLAUDE_CODE_AUTO_MODE_SERVER=0. A user settings.json env block would undo the
// inherited variable, so the client also passes it through --settings, which
// outranks user settings. An explicit process setting is left alone.
func TestClientArgsPinsAutoModeServerThroughSettings(t *testing.T) {
	settingsEnv := func(t *testing.T) map[string]string {
		t.Helper()
		args, err := clientArgs(nil, "default", profile(), "")
		if err != nil {
			t.Fatal(err)
		}
		for i, a := range args {
			if a == "--settings" && i+1 < len(args) {
				var doc struct {
					Env map[string]string `json:"env"`
				}
				if err := json.Unmarshal([]byte(args[i+1]), &doc); err != nil {
					t.Fatal(err)
				}
				return doc.Env
			}
		}
		t.Fatal("no --settings generated")
		return nil
	}
	t.Run("unset", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_AUTO_MODE_SERVER", "")
		os.Unsetenv("CLAUDE_CODE_AUTO_MODE_SERVER")
		if got := settingsEnv(t)["CLAUDE_CODE_AUTO_MODE_SERVER"]; got != "0" {
			t.Fatalf("settings env = %q, want 0", got)
		}
	})
	t.Run("explicit user choice", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_AUTO_MODE_SERVER", "1")
		if got, ok := settingsEnv(t)["CLAUDE_CODE_AUTO_MODE_SERVER"]; ok {
			t.Fatalf("overrode the user's explicit setting with %q", got)
		}
	})
}
