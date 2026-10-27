package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/raketenkater/ggrun/clientkit"
)

const testToken = "0123456789abcdef0123456789abcdef"

func profile() clientkit.Profile {
	return clientkit.Profile{ProtocolVersion: 1, ServerVersion: "test", ModelID: "model-a", ModelName: "fixture", MainAlias: "local", UtilityAlias: "local-fast", ContextPerSlot: 65536, Slots: 1, Policy: clientkit.Policy{Version: 1, CompactWindow: 65536, CompactPercent: 75, Effort: "xhigh", TimeoutMS: clientkit.MaxTimeoutMS, WorkflowHook: true}}
}
func home(t *testing.T) {
	t.Helper()
	d := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", d)
	t.Setenv("AppData", d)
	t.Setenv("HOME", d)
	t.Setenv("USERPROFILE", d)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
}
func fixture(t *testing.T, p *clientkit.Profile) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/prefix/ggrun/client":
			json.NewEncoder(w).Encode(p)
		case "/prefix/health":
			io.WriteString(w, `{"status":"ok"}`)
		case "/prefix/ggrun/router":
			io.WriteString(w, `{"active":1,"queued":2,"limit":1}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	return s
}
func TestConnectUsesGenericPrefixAndPrivateProfile(t *testing.T) {
	home(t)
	p := profile()
	s := fixture(t, &p)
	t.Setenv("GGRUN_AUTH_TOKEN", testToken)
	if err := connect([]string{s.URL + "/prefix/", "--name", "work"}); err != nil {
		t.Fatal(err)
	}
	c, err := load("work")
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != s.URL+"/prefix" || c.Token != testToken {
		t.Fatal("connection changed")
	}
	if err = connect([]string{s.URL + "/prefix", "--name", "work"}); err == nil {
		t.Fatal("silently overwrote connection")
	}
	path, _ := connectionPath("work")
	st, _ := os.Stat(path)
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0600 {
		t.Fatal("profile not private")
	}
	if runtime.GOOS == "windows" {
		b, _ := os.ReadFile(path)
		if bytes.Contains(b, []byte(testToken)) {
			t.Fatal("Windows token stored as plaintext")
		}
	}
	if _, err = connectionPath("../../escape"); err == nil {
		t.Fatal("path traversal accepted")
	}
}
func TestRedirectNeverForwardsCredentials(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	_, err := inspect(connection{URL: redirect.URL, Token: testToken})
	if err == nil || !strings.Contains(err.Error(), "redirect") || called {
		t.Fatalf("redirect credentials escaped: %v %t", err, called)
	}
}
func TestReadinessAndProtocolFailures(t *testing.T) {
	for _, code := range []int{401, 403, 503, 404} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		if _, err := inspect(connection{URL: s.URL, Token: testToken}); err == nil {
			t.Errorf("accepted %d", code)
		}
		s.Close()
	}
	p := profile()
	p.ProtocolVersion = 999
	s := fixture(t, &p)
	if _, err := inspect(connection{URL: s.URL + "/prefix", Token: testToken}); err == nil {
		t.Fatal("unknown protocol accepted")
	}
}
func TestResumeRequiresSameModelAndCapacity(t *testing.T) {
	home(t)
	path, _ := sessionPath("default")
	p := profile()
	s, err := newSession(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveSession(path, s); err != nil {
		t.Fatal(err)
	}
	if _, err = resumedSession(path, "", p); err != nil {
		t.Fatal(err)
	}
	p.ModelID = "model-b"
	if _, err = resumedSession(path, "", p); err == nil {
		t.Fatal("changed model accepted")
	}
	p.ModelID = s.ModelID
	p.ContextPerSlot = s.Context / 2
	if _, err = resumedSession(path, "", p); err == nil {
		t.Fatal("smaller context accepted")
	}
	if err = saveSession(path, s); err != nil {
		t.Fatal("could not replace session:", err)
	}
	p.ContextPerSlot = s.Context * 2
	grown, err := resumedSession(path, "", p)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveSession(path, grown); err != nil {
		t.Fatal(err)
	}
	p.ContextPerSlot = s.Context
	if _, err = resumedSession(path, "", p); err == nil {
		t.Fatal("forgot the larger capacity used by the resumed session")
	}
}
func TestWorkflowHookUpdatesWithoutGrantingPermission(t *testing.T) {
	var out bytes.Buffer
	input := `{"tool_name":"Workflow","tool_input":{"script":"await agent('check');"}}`
	if err := workflowHook(strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2147483647") || strings.Contains(out.String(), `"permissionDecision":"allow"`) {
		t.Fatal(out.String())
	}
	out.Reset()
	if err := workflowHook(strings.NewReader(`{"tool_name":"Workflow","tool_input":{"name":"missing"}}`), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
		t.Fatal(out.String())
	}
}
func TestClientKeepsCustomSettingsAndPermissionChoice(t *testing.T) {
	home(t)
	args, err := clientArgs([]string{"--settings", `{"statusLine":{"command":"mine"}}`, "--permission-mode", "default", "--mcp-config", "mine.json"}, "default", profile())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	if strings.Count(joined, "--settings") != 1 || strings.Contains(joined, "workflow-hook") || strings.Contains(joined, "--permission-mode\nauto") || strings.Contains(joined, "duckduckgo-mcp-server") {
		t.Fatal(args)
	}
}

func TestConflictingSavedSettingsAreDiagnosedWithoutSecrets(t *testing.T) {
	home(t)
	// Explicit inline settings exercise the same validation without changing
	// the repository or a user's real settings files.
	c := connection{URL: "https://models.example", Token: testToken}
	err := settingsConflict(c, profile(), []string{"--settings", `{"env":{"ANTHROPIC_AUTH_TOKEN":"private-old-token"}}`})
	if err == nil || strings.Contains(err.Error(), "private-old-token") {
		t.Fatal("conflict missing or leaked a token", err)
	}
	err = settingsConflict(c, profile(), []string{"--settings", `{"hooks":{"SessionStart":[]},"env":{"MY_SETTING":"kept"}}`})
	if err != nil {
		t.Fatal(err)
	}
}

// The helper is this test executable under a temporary claude(.exe) name, so
// child-process ownership tests run natively on Linux and Windows without a shell.
func TestMain(m *testing.M) {
	if os.Getenv("GGRUN_CLIENT_TEST_CHILD") == "1" {
		if len(os.Args) > 1 && os.Args[1] == "--version" {
			io.WriteString(os.Stdout, targetClaudeVersion+" (Claude Code)\n")
			os.Exit(0)
		}
		cwd, _ := os.Getwd()
		b, _ := json.Marshal(map[string]any{"args": os.Args[1:], "env": os.Environ(), "cwd": cwd})
		if err := os.WriteFile(os.Getenv("GGRUN_CLIENT_TEST_RECEIPT"), b, 0600); err != nil {
			os.Exit(7)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestLaunchRunsToolsOnClientAndScopesEnvironment(t *testing.T) {
	home(t)
	p := profile()
	s := fixture(t, &p)
	t.Setenv("GGRUN_AUTH_TOKEN", testToken)
	if err := connect([]string{s.URL + "/prefix"}); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	exe, _ := os.Executable()
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err = os.WriteFile(filepath.Join(binDir, name), b, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	receipt := filepath.Join(t.TempDir(), "receipt.json")
	t.Setenv("GGRUN_CLIENT_TEST_CHILD", "1")
	t.Setenv("GGRUN_CLIENT_TEST_RECEIPT", receipt)
	t.Setenv("ANTHROPIC_API_KEY", "do-not-forward")
	for _, k := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		t.Setenv(k, "")
	}
	if err = launch([]string{"--", "-p", "test"}); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Args, Env []string
		Cwd       string
	}
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if got.Cwd != cwd {
		t.Fatalf("child left project: %s", got.Cwd)
	}
	env := strings.Join(got.Env, "\n")
	if !strings.Contains(env, "ANTHROPIC_BASE_URL="+s.URL+"/prefix") || strings.Contains(env, "do-not-forward") || !strings.Contains(env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS=65536") {
		t.Fatal("incorrect child environment")
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "do-not-forward" {
		t.Fatal("parent environment mutated")
	}
	if err = launch([]string{"--resume", "--", "-p", "continue"}); err != nil {
		t.Fatal(err)
	}
	if _, err = inspect(connection{URL: s.URL + "/prefix", Token: testToken}); err != nil {
		t.Fatal("client exit stopped server")
	}
}
