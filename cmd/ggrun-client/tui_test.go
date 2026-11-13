package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/raketenkater/ggrun/clientkit"
)

func saveTestConnection(t *testing.T, name, url string) {
	t.Helper()
	b, _ := json.Marshal(connection{Version: 1, URL: url, Token: testToken, Profile: profile()})
	if err := clientkit.WritePrivate(mustConnectionPath(name), b); err != nil {
		t.Fatal(err)
	}
}

func key(m *tuiModel, k string) {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	m.Update(msg)
}

func pick(t *testing.T, m *tuiModel, prefix string) {
	t.Helper()
	for i, it := range m.items {
		if strings.HasPrefix(it.label, prefix) {
			m.cursor = i
			key(m, "enter")
			return
		}
	}
	t.Fatalf("no menu item %q in %v", prefix, m.View())
}

func TestTUIStartsNewSessionWithSavedSettings(t *testing.T) {
	home(t)
	saveTestConnection(t, "server", "https://example.invalid")
	m := newTUIModel()
	m.Update(statusMsg{"server", serverState{name: "server", ok: true, status: "ready", model: "fixture", ctx: 262144}})
	m.Update(claudeMsg{controlsClaudeVersion, true})
	v := m.View()
	for _, want := range []string{"server", "ready", "fixture", "256k context", "New session in", "/goal"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Install Claude Code") {
		t.Fatal("qualified Claude offered for install")
	}
	pick(t, m, "New session in")
	if got := strings.Join(m.action.args, " "); m.action.kind != "launch" || got != "--connection server" {
		t.Fatalf("launch: %+v", m.action)
	}
	if s := loadSettings(); s.Server != "server" {
		t.Fatalf("last server not remembered: %+v", s)
	}
}

func TestTUIRefusesNotReadyServerAndOffersInstall(t *testing.T) {
	home(t)
	saveTestConnection(t, "server", "https://example.invalid")
	m := newTUIModel()
	m.Update(statusMsg{"server", serverState{name: "server", status: "token rejected — choose Update token"}})
	m.Update(claudeMsg{"2.1.300", false})
	pick(t, m, "New session in")
	if m.action.kind != "" || !strings.Contains(m.message, "token rejected") {
		t.Fatalf("launched against a server that is not ready: %+v %q", m.action, m.message)
	}
	pick(t, m, "Update Claude Code")
	if m.action.kind != "install" {
		t.Fatalf("install not offered: %+v", m.action)
	}
}

func TestTUIResumesRecentProjectsAndCyclesReview(t *testing.T) {
	home(t)
	saveTestConnection(t, "server", "https://example.invalid")
	here, other := t.TempDir(), t.TempDir()
	for _, dir := range []string{here, other} {
		old, _ := os.Getwd()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		p, _ := sessionPath("server")
		resolved, _ := filepath.EvalSymlinks(dir)
		if err := saveSession(p, session{ID: "11111111-2222-4333-8444-555555555555", ModelID: "model-a", Context: 65536, Version: 1, Dir: resolved}); err != nil {
			t.Fatal(err)
		}
		os.Chdir(old)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	os.Chdir(here)
	m := newTUIModel()
	m.Update(statusMsg{"server", serverState{name: "server", ok: true, status: "ready"}})
	pick(t, m, "Resume last session here")
	if m.action.args[len(m.action.args)-1] != "--resume" || m.action.dir != "" {
		t.Fatalf("resume here: %+v", m.action)
	}
	m.action = tuiAction{}
	otherResolved, _ := filepath.EvalSymlinks(other)
	pick(t, m, "Resume "+tildePath(otherResolved))
	if m.action.dir != otherResolved || m.action.args[len(m.action.args)-1] != "--resume" {
		t.Fatalf("recent project: %+v", m.action)
	}
	// Review is opt-in, then cycles through bounded budgets.
	m.action = tuiAction{}
	for _, want := range []string{"on · timeout 10m", "on · timeout 30m", "on · timeout 60m", "off", "on · timeout 10m"} {
		pick(t, m, "Main-model review")
		if !strings.Contains(m.View(), "Main-model review: "+want) {
			t.Fatalf("review setting not %q:\n%s", want, m.View())
		}
	}
	if s := loadSettings(); !s.Controls || s.Timeout != "10m" {
		t.Fatalf("settings not saved: %+v", s)
	}
}

func TestNewerClaudeIsAllowedOlderIsNot(t *testing.T) {
	for _, tc := range []struct {
		v, min string
		ok     bool
	}{
		{"2.1.316", "2.1.316", true}, {"2.1.319", "2.1.316", true}, {"2.2.0", "2.1.316", true}, {"3.0.1", "2.1.316", true},
		{"2.1.315", "2.1.316", false}, {"2.0.999", "2.1.316", false}, {"", "2.1.316", false}, {"2.1", "2.1.316", false},
	} {
		if got := claudeAtLeast(tc.v, tc.min); got != tc.ok {
			t.Errorf("claudeAtLeast(%q, %q) = %t", tc.v, tc.min, got)
		}
	}
	home(t)
	saveTestConnection(t, "server", "https://example.invalid")
	m := newTUIModel()
	m.Update(claudeMsg{"2.1.330", true})
	if v := m.View(); strings.Contains(v, "Update Claude Code") || !strings.Contains(v, "newer than tested") {
		t.Fatalf("newer Claude treated as unsupported:\n%s", v)
	}
}
