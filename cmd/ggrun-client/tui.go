package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/raketenkater/ggrun/clientkit"
)

// clientSettings are the few persistent launch choices. No secrets.
type clientSettings struct {
	Controls      bool   `json:"controls"`
	Timeout       string `json:"timeout"`
	AllowUntested bool   `json:"allow_untested,omitempty"`
	Server        string `json:"server,omitempty"` // last selected connection
}

var timeoutChoices = []string{"10m", "30m", "60m"}

func settingsPath() (string, error) {
	root, err := configRoot()
	return filepath.Join(root, "settings.json"), err
}

func loadSettings() clientSettings {
	s := clientSettings{Controls: true, Timeout: "30m"}
	if p, err := settingsPath(); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &s)
		}
	}
	if _, err := time.ParseDuration(s.Timeout); err != nil {
		s.Timeout = "30m"
	}
	return s
}

func saveSettings(s clientSettings) {
	p, err := settingsPath()
	if err != nil {
		return
	}
	if b, err := json.MarshalIndent(s, "", "  "); err == nil && os.MkdirAll(filepath.Dir(p), 0o700) == nil {
		_ = os.WriteFile(p, b, 0o600)
	}
}

type recentSession struct {
	Conn, Dir, ID string
	When          time.Time
}

// recentSessions lists saved connector sessions that know their folder,
// newest first.
func recentSessions() []recentSession {
	root, err := configRoot()
	if err != nil {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(root, "sessions", "*.json"))
	var out []recentSession
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), ".json")
		i := strings.LastIndex(base, "-")
		if i <= 0 || len(base)-i-1 != 32 {
			continue
		}
		b, err := clientkit.ReadPrivate(f)
		if err != nil {
			continue
		}
		var s session
		if json.Unmarshal(b, &s) != nil || s.Dir == "" || s.ID == "" {
			continue
		}
		if st, err := os.Stat(s.Dir); err != nil || !st.IsDir() {
			continue
		}
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		out = append(out, recentSession{Conn: base[:i], Dir: s.Dir, ID: s.ID, When: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	return out
}

// tuiAction is what the TUI asks the outer loop to run with the terminal.
type tuiAction struct {
	kind string // launch, install, quit
	args []string
	dir  string
}

type serverState struct {
	name, url, status, model string
	ctx                      int
	ok                       bool
}

type menuItem struct {
	label string
	run   func(m *tuiModel) tea.Cmd
}

type tuiModel struct {
	settings clientSettings
	servers  []serverState
	sel      int // selected server
	recent   []recentSession
	cwd      string
	claudeV  string
	claudeOK bool
	cursor   int
	items    []menuItem
	input    textinput.Model
	inputFor string // add-url, add-token, token
	pending  string // URL being added
	message  string
	action   tuiAction
	width    int
}

type statusMsg struct {
	name string
	st   serverState
}
type claudeMsg struct {
	v  string
	ok bool
}
type recheckMsg struct{ name string }
type connectedMsg struct {
	name string
	err  error
}

func checkServer(name string) tea.Cmd {
	return func() tea.Msg {
		st := serverState{name: name}
		c, err := load(name)
		if err != nil {
			st.status = "saved connection unreadable"
			return statusMsg{name, st}
		}
		st.url = c.URL
		p, err := inspect(c)
		switch {
		case err == nil:
			st.ok, st.status, st.model, st.ctx = true, "ready", p.ModelName, p.ContextPerSlot
		case errors.Is(err, errLoading):
			st.status = "loading model…"
		case errors.Is(err, errCredential):
			st.status = "token rejected — choose Update token"
		case errors.Is(err, errUnreachable):
			st.status = "unreachable — is the server running / on your VPN?"
		default:
			st.status = err.Error()
		}
		return statusMsg{name, st}
	}
}

func checkClaude() tea.Cmd {
	return func() tea.Msg {
		_, v, err := claudeExecutable(false)
		return claudeMsg{v, err == nil && claudeAtLeast(v, controlsClaudeVersion)}
	}
}

func newTUIModel() *tuiModel {
	m := &tuiModel{settings: loadSettings(), recent: recentSessions(), claudeV: "checking…"}
	m.cwd, _ = os.Getwd()
	if r, err := filepath.EvalSymlinks(m.cwd); err == nil {
		m.cwd = r
	}
	names, _ := savedConnections()
	for i, n := range names {
		m.servers = append(m.servers, serverState{name: n, status: "checking…"})
		if n == m.settings.Server {
			m.sel = i
		}
	}
	m.input = textinput.New()
	m.rebuild()
	return m
}

func (m *tuiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{checkClaude()}
	for _, s := range m.servers {
		cmds = append(cmds, checkServer(s.name))
	}
	return tea.Batch(cmds...)
}

func (m *tuiModel) server() *serverState {
	if len(m.servers) == 0 {
		return nil
	}
	return &m.servers[m.sel]
}

func tildePath(path string) string {
	if h, err := os.UserHomeDir(); err == nil && h != "" && strings.HasPrefix(path, h) {
		return "~" + strings.TrimPrefix(path, h)
	}
	return path
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func (m *tuiModel) launchArgs(conn string, resume bool) []string {
	args := []string{"--connection", conn}
	if m.settings.Controls {
		args = append(args, "--local-controls", "--control-timeout", m.settings.Timeout)
	}
	if m.settings.AllowUntested {
		args = append(args, "--allow-untested-claude")
	}
	if resume {
		args = append(args, "--resume")
	}
	return args
}

func (m *tuiModel) startAction(a tuiAction) tea.Cmd {
	m.action = a
	if s := m.server(); s != nil {
		m.settings.Server = s.name
	}
	saveSettings(m.settings)
	return tea.Quit
}

// rebuild recomputes the menu from current state.
func (m *tuiModel) rebuild() {
	m.items = nil
	add := func(label string, run func(*tuiModel) tea.Cmd) { m.items = append(m.items, menuItem{label, run}) }
	s := m.server()
	if s != nil {
		conn := s.name
		add("New session in "+tildePath(m.cwd), func(m *tuiModel) tea.Cmd {
			if !m.server().ok {
				m.message = "Server is not ready: " + m.server().status
				return nil
			}
			return m.startAction(tuiAction{kind: "launch", args: m.launchArgs(conn, false)})
		})
		for _, r := range m.recent {
			if r.Dir == m.cwd && r.Conn == conn {
				add("Resume last session here ("+ago(r.When)+")", func(m *tuiModel) tea.Cmd {
					return m.startAction(tuiAction{kind: "launch", args: m.launchArgs(conn, true)})
				})
				break
			}
		}
		shown := 0
		for _, r := range m.recent {
			if r.Dir == m.cwd || shown == 5 {
				continue
			}
			shown++
			r := r
			add(fmt.Sprintf("Resume %s · %s · %s", tildePath(r.Dir), r.Conn, ago(r.When)), func(m *tuiModel) tea.Cmd {
				return m.startAction(tuiAction{kind: "launch", dir: r.Dir, args: m.launchArgs(r.Conn, true)})
			})
		}
	}
	if !m.claudeOK && m.claudeV != "checking…" {
		add("Update Claude Code to "+controlsClaudeVersion+" (needed for main-model review)", func(m *tuiModel) tea.Cmd {
			return m.startAction(tuiAction{kind: "install"})
		})
	}
	add("Add server…", func(m *tuiModel) tea.Cmd { return m.prompt("add-url", "Server URL (https://…)", false) })
	if s != nil {
		add("Update token for "+s.name+"…", func(m *tuiModel) tea.Cmd {
			return m.prompt("token", "Gateway token (on the server: ggrun gateway-token)", true)
		})
	}
	review := "off"
	if m.settings.Controls {
		review = "on · timeout " + m.settings.Timeout
	}
	add("Main-model review: "+review, func(m *tuiModel) tea.Cmd {
		switch {
		case !m.settings.Controls:
			m.settings.Controls, m.settings.Timeout = true, timeoutChoices[0]
		default:
			i := 0
			for j, t := range timeoutChoices {
				if t == m.settings.Timeout {
					i = j
				}
			}
			if i+1 < len(timeoutChoices) {
				m.settings.Timeout = timeoutChoices[i+1]
			} else {
				m.settings.Controls = false
			}
		}
		saveSettings(m.settings)
		return nil
	})
	add("Quit", func(m *tuiModel) tea.Cmd { m.action = tuiAction{kind: "quit"}; return tea.Quit })
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
}

func (m *tuiModel) prompt(kind, placeholder string, secret bool) tea.Cmd {
	m.inputFor = kind
	m.input = textinput.New()
	m.input.Placeholder = placeholder
	m.input.CharLimit = 4096
	m.input.Width = 60
	if secret {
		m.input.EchoMode = textinput.EchoPassword
		m.input.EchoCharacter = '•'
	}
	m.message = ""
	return m.input.Focus()
}

func (m *tuiModel) submit(value string) tea.Cmd {
	value = strings.TrimSpace(value)
	kind := m.inputFor
	m.inputFor = ""
	switch kind {
	case "add-url":
		base, err := clientkit.BaseURL(value, false)
		if err != nil {
			m.message = err.Error()
			return nil
		}
		m.pending = base
		return m.prompt("add-token", "Gateway token (on the server: ggrun gateway-token)", true)
	case "add-token":
		name := "server"
		for i := 2; ; i++ {
			if _, err := os.Stat(mustConnectionPath(name)); os.IsNotExist(err) {
				break
			}
			name = fmt.Sprintf("server%d", i)
		}
		url := m.pending
		m.message = "Connecting " + url + " (waits while the model loads)…"
		return func() tea.Msg {
			c := connection{Version: 1, URL: url, Token: value}
			if len(value) < 32 || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
				return connectedMsg{name, fmt.Errorf("gateway token must be 32–4096 characters on one line")}
			}
			p, err := guidedReady(c, nopWriter{})
			if err != nil {
				return connectedMsg{name, err}
			}
			c.Profile = p
			b, _ := json.Marshal(c)
			return connectedMsg{name, clientkit.WritePrivate(mustConnectionPath(name), b)}
		}
	case "token":
		s := m.server()
		if s == nil {
			return nil
		}
		name := s.name
		m.message = "Checking the new token…"
		return func() tea.Msg {
			c, err := load(name)
			if err == nil {
				_, err = updateToken(name, c, value)
			}
			return connectedMsg{name, err}
		}
	}
	return nil
}

type nopWriter struct{}

func (nopWriter) Write(b []byte) (int, error) { return len(b), nil }

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case claudeMsg:
		m.claudeV, m.claudeOK = msg.v, msg.ok
		if msg.v == "" {
			m.claudeV = "not installed"
		}
		m.rebuild()
	case statusMsg:
		for i := range m.servers {
			if m.servers[i].name == msg.name {
				m.servers[i] = msg.st
			}
		}
		if msg.st.status == "loading model…" {
			name := msg.name
			return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return recheckMsg{name} })
		}
	case recheckMsg:
		return m, checkServer(msg.name)
	case connectedMsg:
		if msg.err != nil {
			m.message = "Not saved: " + msg.err.Error()
			return m, nil
		}
		m.message = "Saved " + msg.name + "."
		found := false
		for i, s := range m.servers {
			if s.name == msg.name {
				m.sel, found = i, true
			}
		}
		if !found {
			m.servers = append(m.servers, serverState{name: msg.name, status: "checking…"})
			m.sel = len(m.servers) - 1
		}
		m.rebuild()
		return m, checkServer(msg.name)
	case tea.KeyMsg:
		if m.inputFor != "" {
			switch msg.String() {
			case "esc":
				m.inputFor, m.message = "", ""
				return m, nil
			case "enter":
				return m, m.submit(m.input.Value())
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		switch msg.String() {
		case "ctrl+c", "q":
			m.action = tuiAction{kind: "quit"}
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "left", "h", "right", "l", "tab":
			if len(m.servers) > 1 {
				if msg.String() == "left" || msg.String() == "h" {
					m.sel = (m.sel + len(m.servers) - 1) % len(m.servers)
				} else {
					m.sel = (m.sel + 1) % len(m.servers)
				}
				m.rebuild()
			}
		case "r":
			m.recent = recentSessions()
			m.rebuild()
			var cmds []tea.Cmd
			for _, s := range m.servers {
				cmds = append(cmds, checkServer(s.name))
			}
			return m, tea.Batch(cmds...)
		case "enter", " ":
			if len(m.items) > 0 {
				cmd := m.items[m.cursor].run(m)
				m.rebuild()
				return m, cmd
			}
		}
	}
	return m, nil
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	badStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	selStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
)

func (m *tuiModel) View() string {
	var b strings.Builder
	cv := "Claude Code " + m.claudeV
	switch {
	case m.claudeOK && qualifiedClaude(m.claudeV):
		cv = okStyle.Render(cv + " ✓")
	case m.claudeOK:
		cv = okStyle.Render(cv) + dimStyle.Render(" (newer than tested "+controlsClaudeVersion+")")
	case m.claudeV != "checking…":
		cv = badStyle.Render(cv + " (needs " + controlsClaudeVersion + " or newer)")
	}
	fmt.Fprintf(&b, "%s %s · %s\n\n", titleStyle.Render("ggrun-client"), version, cv)
	if s := m.server(); s != nil {
		st := badStyle.Render(s.status)
		if s.ok {
			st = okStyle.Render("ready") + fmt.Sprintf(" · %s · %dk context", s.model, s.ctx/1024)
		}
		hint := ""
		if len(m.servers) > 1 {
			hint = dimStyle.Render(fmt.Sprintf("   ←/→ switch (%d/%d)", m.sel+1, len(m.servers)))
		}
		fmt.Fprintf(&b, "Server  %s  %s\n        %s%s\n\n", titleStyle.Render(s.name), dimStyle.Render(s.url), st, hint)
	} else {
		b.WriteString(badStyle.Render("No server yet — choose Add server.") + "\n\n")
	}
	for i, it := range m.items {
		if i == m.cursor {
			b.WriteString(selStyle.Render("▶ "+it.label) + "\n")
		} else {
			b.WriteString("  " + it.label + "\n")
		}
	}
	if m.inputFor != "" {
		b.WriteString("\n" + m.input.View() + "\n" + dimStyle.Render("Enter to confirm · Esc to cancel") + "\n")
	}
	if m.message != "" {
		b.WriteString("\n" + m.message + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("↑/↓ move · Enter select · r refresh · q quit · in Claude use /goal <condition> for goals") + "\n")
	return b.String()
}

// runTUI shows the menu, runs the chosen action with the full terminal, and
// returns to the menu when Claude exits.
func runTUI() error {
	for {
		m := newTUIModel()
		final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
		if err != nil {
			return err
		}
		a := final.(*tuiModel).action
		switch a.kind {
		case "launch":
			start, _ := os.Getwd()
			if a.dir != "" {
				if err := os.Chdir(a.dir); err != nil {
					fmt.Fprintln(os.Stderr, "ggrun-client:", err)
					continue
				}
			}
			if err := startClaude(a.args); err != nil {
				var exit *childExit
				if !errors.As(err, &exit) {
					fmt.Fprintln(os.Stderr, "ggrun-client:", err)
					fmt.Fprint(os.Stderr, "Press Enter to return to the menu…")
					fmt.Scanln()
				}
			}
			_ = os.Chdir(start)
		case "install":
			if err := installClaude(controlsClaudeVersion); err != nil {
				fmt.Fprintln(os.Stderr, "ggrun-client: claude install:", err)
				fmt.Fprint(os.Stderr, "Press Enter to return to the menu…")
				fmt.Scanln()
			}
		default:
			return nil
		}
	}
}
