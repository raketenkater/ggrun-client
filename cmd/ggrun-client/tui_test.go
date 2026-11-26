package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		pick(t, m, "Extra tool review")
		if !strings.Contains(m.View(), "Extra tool review: "+want) {
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

// step runs one tea.Cmd, failing the test if it blocks.
func step(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("command blocked")
		return nil
	}
}

// follow feeds a wait job's messages into the model the way bubbletea does
// and returns the status line shown after each "still loading" update.
func follow(t *testing.T, m *tuiModel, cmd tea.Cmd) []string {
	t.Helper()
	var shown []string
	for i := 0; cmd != nil && i < 200; i++ {
		msg := step(t, cmd)
		if msg == nil {
			break
		}
		_, cmd = m.Update(msg)
		switch msg.(type) {
		case loadingMsg:
			shown = append(shown, m.message)
		case connectedMsg:
			return shown
		}
	}
	return shown
}

func submitToken(t *testing.T, m *tuiModel, token string) tea.Cmd {
	t.Helper()
	pick(t, m, "Update token for")
	m.input.SetValue(token)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return cmd
}

func TestTUITokenCheckShowsLoadingThenCompletes(t *testing.T) {
	home(t)
	newToken := strings.Repeat("n", 40)
	s := gatewayFixture(t, newToken, 3) // 503 three times, then ready
	stubGuided(t)
	saveTestConnection(t, "server", s.URL)
	m := newTUIModel()
	cmd := submitToken(t, m, newToken)
	if !strings.Contains(m.message, "Checking the new token") || m.job == nil {
		t.Fatalf("check not started: %q", m.message)
	}
	shown := follow(t, m, cmd)
	if len(shown) < 3 {
		t.Fatalf("loading not shown on each retry: %q", shown)
	}
	for _, want := range []string{"still loading the model", "not been checked or saved", "Esc to cancel"} {
		if !strings.Contains(shown[0], want) {
			t.Fatalf("loading status lacks %q: %q", want, shown[0])
		}
	}
	if strings.Contains(shown[0], "accepted") {
		t.Fatalf("a 503 does not accept the token: %q", shown[0])
	}
	if m.message != "Saved server." || m.job != nil {
		t.Fatalf("check did not finish by itself: %q job=%v", m.message, m.job)
	}
	if c, err := load("server"); err != nil || c.Token != newToken {
		t.Fatalf("token not saved: %v", err)
	}
}

func TestTUITokenCheckGivesUpAfterTheBound(t *testing.T) {
	home(t)
	newToken := strings.Repeat("n", 40)
	s := gatewayFixture(t, newToken, 1<<40) // never ready
	stubGuided(t)
	oldLimit := loadingLimit
	loadingLimit = 80 * time.Millisecond
	t.Cleanup(func() { loadingLimit = oldLimit })
	saveTestConnection(t, "server", s.URL)
	m := newTUIModel()
	follow(t, m, submitToken(t, m, newToken))
	if !strings.Contains(m.message, "Not saved") || !strings.Contains(m.message, "still loading after 80ms") || m.job != nil {
		t.Fatalf("wait not bounded: %q job=%v", m.message, m.job)
	}
	if c, _ := load("server"); c.Token != testToken {
		t.Fatal("token saved although the server never became ready")
	}
}

func TestTUITokenCheckEscCancelsTheWait(t *testing.T) {
	home(t)
	newToken := strings.Repeat("n", 40)
	s := gatewayFixture(t, newToken, 1<<40)
	stubGuided(t)
	saveTestConnection(t, "server", s.URL)
	m := newTUIModel()
	cmd := submitToken(t, m, newToken)
	msg := step(t, cmd)
	if _, ok := msg.(loadingMsg); !ok {
		t.Fatalf("expected a loading update, got %T", msg)
	}
	_, next := m.Update(msg)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.job != nil || !strings.Contains(m.message, "Cancelled") {
		t.Fatalf("Esc did not cancel: %q job=%v", m.message, m.job)
	}
	if got := step(t, next); got != nil {
		t.Fatalf("cancelled wait still reports: %T", got)
	}
	if c, _ := load("server"); c.Token != testToken {
		t.Fatal("cancelled check saved the token")
	}
}

func TestTUIAddServerShowsLoadingThenSaves(t *testing.T) {
	home(t)
	s := gatewayFixture(t, testToken, 2)
	stubGuided(t)
	m := newTUIModel()
	pick(t, m, "Add server")
	m.input.SetValue(s.URL)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.input.SetValue(testToken)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	shown := follow(t, m, cmd)
	if len(shown) == 0 || !strings.Contains(shown[0], "still loading the model") || !strings.Contains(shown[0], "nothing is saved yet") {
		t.Fatalf("loading not shown while adding: %q", shown)
	}
	if c, err := load("server"); err != nil || c.URL != s.URL || m.message != "Saved server." {
		t.Fatalf("server not added: %v %q", err, m.message)
	}
}

func TestRejectedTokenSaysTheServerMayHaveANewOne(t *testing.T) {
	home(t)
	s := gatewayFixture(t, strings.Repeat("n", 40), 0) // the server's token changed
	saveTestConnection(t, "server", s.URL)
	st := checkServer("server")().(statusMsg).st.status
	for _, want := range []string{"token rejected", "new token", "ggrun gateway-token", "Update token"} {
		if !strings.Contains(st, want) {
			t.Fatalf("status lacks %q: %q", want, st)
		}
	}
	_, err := inspect(connection{Version: 1, URL: s.URL, Token: testToken})
	if !errors.Is(err, errCredential) || !strings.Contains(err.Error(), "ggrun gateway-token") || !strings.Contains(err.Error(), "new gateway token") {
		t.Fatalf("CLI error gives no rotation hint: %v", err)
	}
}
