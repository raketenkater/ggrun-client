package main

import (
	"encoding/json"
	"github.com/raketenkater/ggrun/clientkit"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGoalOnlyLaunchBindsWithoutToolReview(t *testing.T) {
	home(t)
	fakeClaude(t)
	p := profile()
	gw := fixture(t, &p)
	t.Setenv("GGRUN_AUTH_TOKEN", testToken)
	if err := connect([]string{gw.URL + "/prefix"}); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(t.TempDir(), "receipt.json")
	t.Setenv("GGRUN_CLIENT_TEST_RECEIPT", receipt)
	file := filepath.Join(t.TempDir(), "goal.txt")
	os.WriteFile(file, []byte("Create the authorized fixture marker."), 0600)
	for _, resume := range []bool{false, true} {
		args := []string{"--local-controls", "--goal-only", "--goal-file", file, "--allow-untested-claude"}
		if resume {
			args = append(args, "--resume")
		}
		args = append(args, "--", "--mcp-config", "{}")
		if err := launch(args); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(receipt)
		var child struct{ Args []string }
		json.Unmarshal(b, &child)
		var settingsPath string
		for i, a := range child.Args {
			if a == "--settings" {
				settingsPath = child.Args[i+1]
			}
		}
		b, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatal(err)
		}
		var settings struct {
			Hooks map[string][]struct {
				Matcher string
				Hooks   []struct {
					Command string
					Timeout int
				}
			}
		}
		json.Unmarshal(b, &settings)
		if len(settings.Hooks["Stop"]) != 1 || settings.Hooks["Stop"][0].Hooks[0].Timeout != 630 {
			t.Fatalf("goal missing bounded Stop: %s", b)
		}
		if len(settings.Hooks["StopFailure"]) != 1 || !strings.Contains(settings.Hooks["Stop"][0].Hooks[0].Command, "--run-id") {
			t.Fatal("goal hooks do not track failures and launch ownership")
		}
		for _, h := range settings.Hooks["PreToolUse"] {
			if h.Matcher == ".*" {
				t.Fatal("goal-only installed per-tool review")
			}
		}
		sp, _ := sessionPath("default")
		sb, _ := clientkit.ReadPrivate(sp)
		var s session
		json.Unmarshal(sb, &s)
		gp, _ := goalFile("default")
		g, err := readGoal(gp)
		if err != nil || g.Session != s.ID || s.Controls || !s.Goals || g.State != "paused" || g.RunID == "" || g.RunID != s.RunID {
			t.Fatalf("goal binding: %+v %+v %v", s, g, err)
		}
	}
}

func TestGoalAndReviewSettingsAreIndependent(t *testing.T) {
	home(t)
	input := []string{"--permission-mode", "auto", "--settings", `{"permissions":{"deny":["Bash(rm *)"]},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"user-policy"}]}]}}`}
	for _, review := range []bool{false, true} {
		args := input
		var err error
		if review {
			args, err = controlArgs(args, "fixture", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
		}
		args, err = configuredControlArgs(args, "fixture", time.Minute, []string{"Stop"})
		if err != nil {
			t.Fatal(err)
		}
		var path string
		for i, a := range args {
			if a == "--settings" {
				path = args[i+1]
			}
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), "user-policy") || !strings.Contains(string(b), "Bash(rm *)") || strings.Contains(string(b), "UserPromptSubmit") {
			t.Fatal("user policy changed or duplicate controller registered")
		}
		var doc struct {
			Hooks map[string][]struct{ Matcher string }
		}
		json.Unmarshal(b, &doc)
		n := 0
		for _, h := range doc.Hooks["PreToolUse"] {
			if h.Matcher == ".*" {
				n++
			}
		}
		if (n == 1) != review || len(doc.Hooks["Stop"]) != 1 {
			t.Fatalf("coupled hook policy: %s", b)
		}
	}
}

func TestLegacyControlsSettingDoesNotOptIntoReview(t *testing.T) {
	home(t)
	p, _ := settingsPath()
	os.MkdirAll(filepath.Dir(p), 0700)
	os.WriteFile(p, []byte(`{"controls":true,"timeout":"30m"}`), 0600)
	if loadSettings().Controls {
		t.Fatal("ambiguous old goal-controls setting enabled tool review")
	}
	saveSettings(clientSettings{Controls: true, Timeout: "10m"})
	if !loadSettings().Controls {
		t.Fatal("explicit review choice not persisted")
	}
}

func TestTUIGoalFileDoesNotEnableReview(t *testing.T) {
	home(t)
	m := newTUIModel()
	f := filepath.Join(t.TempDir(), "goal.txt")
	os.WriteFile(f, []byte("A verified fixture."), 0600)
	m.inputFor = "goal-file"
	m.submit(f)
	args := m.launchArgs("fixture", false)
	if m.settings.Controls || !hasFlag(args, "--goal-only") || !hasFlag(args, "--goal-file") || hasFlag(args, "--local-controls") {
		t.Fatalf("TUI goal enabled review: %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "Read the user-selected completion condition file") {
		t.Fatal("TUI goal launch has no initial work instruction")
	}
	m.inputFor = "goal-file"
	m.submit("")
	if hasFlag(m.launchArgs("fixture", false), "--goal-file") {
		t.Fatal("goal selection could not be cleared")
	}
}

func TestNoToolReviewOverridesLegacyFlag(t *testing.T) {
	home(t)
	fakeClaude(t)
	p := profile()
	gw := fixture(t, &p)
	t.Setenv("GGRUN_AUTH_TOKEN", testToken)
	if err := connect([]string{gw.URL + "/prefix"}); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(t.TempDir(), "child.json")
	t.Setenv("GGRUN_CLIENT_TEST_RECEIPT", receipt)
	if err := launch([]string{"--local-controls", "--no-tool-review", "--", "--mcp-config", "{}"}); err != nil {
		t.Fatal(err)
	}
	sp, _ := sessionPath("default")
	b, _ := clientkit.ReadPrivate(sp)
	var s session
	json.Unmarshal(b, &s)
	if s.Controls || s.Goals {
		t.Fatal("disabling tool review enabled a control hook")
	}
	if err := launch([]string{"--goal-only"}); err == nil {
		t.Fatal("goal-only without a condition accepted")
	}
}
