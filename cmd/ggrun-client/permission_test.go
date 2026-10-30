package main

import (
	"strings"
	"testing"
)

// A long general API timeout does not extend native Claude's classifier
// deadline. Never silently select self-classification for an unqualified lane.
func TestPermissionDefaultFollowsAdvertisedWorker(t *testing.T) {
	home(t)
	t.Setenv("GGRUN_CLAUDE_PERMISSION_MODE", "")
	for _, worker := range []bool{false, true} {
		p := profile()
		p.SeparateWorker = worker
		args, err := clientArgs([]string{"--settings", "{}", "--mcp-config", "{}"}, "default", p)
		if err != nil {
			t.Fatal(err)
		}
		want := "default"
		if worker {
			want = "auto"
		}
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
		{"explicit auto", "", "auto", []string{"--permission-mode", "auto"}},
		{"inline explicit", "auto", "plan", []string{"--permission-mode=plan"}},
		{"environment", "auto", "auto", nil},
		{"inherit", "inherit", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GGRUN_CLAUDE_PERMISSION_MODE", tc.env)
			extra := append([]string{"--settings", "{}", "--mcp-config", "{}"}, tc.extra...)
			args, err := clientArgs(extra, "default", profile())
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
