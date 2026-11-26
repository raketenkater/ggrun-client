package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/raketenkater/ggrun/clientkit"
)

func TestDoctorCompactionLine(t *testing.T) {
	home(t)
	t.Setenv("CLAUDE_CODE_MAX_OUTPUT_TOKENS", "")
	for _, tc := range []struct {
		ctx  int
		want string
	}{{40960, "19768 of 40960"}, {262144, ""}} {
		p := profile()
		p.ContextPerSlot, p.Policy.CompactWindow = tc.ctx, tc.ctx
		env, err := claudeEnvironment(p, "http://127.0.0.1", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		at, window := clientkit.CompactionEstimate(env)
		want := tc.want
		if want == "" {
			want = strings.TrimSpace(strings.Join([]string{itoa(at), "of", itoa(window)}, " "))
			if window == 0 {
				t.Fatal("no estimate for default budget")
			}
		}
		got := compactionLine(env)
		if got != "Claude Code compacts at about "+want+" tokens (estimate)\n" {
			t.Fatalf("ctx %d: %q", tc.ctx, got)
		}
		t.Logf("ctx %d: %s", tc.ctx, strings.TrimSpace(got))
	}
	if compactionLine([]string{"A=b"}) != "" {
		t.Fatal("printed a line without a window")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
