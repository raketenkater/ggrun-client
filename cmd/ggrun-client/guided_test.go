package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raketenkater/ggrun/clientkit"
)

// fakeClaude puts this test binary on PATH as claude; it reports the
// initial target version (not the controls version) for --version.
func fakeClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	exe, _ := os.Executable()
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err = os.WriteFile(filepath.Join(dir, name), b, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GGRUN_CLIENT_TEST_CHILD", "1")
}

type guidedStubs struct {
	secrets   []string
	installed []string
	launched  [][]string
}

func stubGuided(t *testing.T, secrets ...string) *guidedStubs {
	t.Helper()
	g := &guidedStubs{secrets: secrets}
	oldSecret, oldInstall, oldStart, oldRetry := readSecret, installClaude, startClaude, loadingRetry
	readSecret = func(string) (string, error) {
		if len(g.secrets) == 0 {
			t.Fatal("unexpected token prompt")
		}
		s := g.secrets[0]
		g.secrets = g.secrets[1:]
		return s, nil
	}
	installClaude = func(v string) error { g.installed = append(g.installed, v); return nil }
	startClaude = func(args []string) error { g.launched = append(g.launched, args); return nil }
	loadingRetry = 10 * time.Millisecond
	t.Cleanup(func() {
		readSecret, installClaude, startClaude, loadingRetry = oldSecret, oldInstall, oldStart, oldRetry
	})
	return g
}

// gatewayFixture accepts token, reports loading for the first `loading` calls.
func gatewayFixture(t *testing.T, token string, loading int64) *httptest.Server {
	t.Helper()
	p := profile()
	var calls atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		if calls.Add(1) <= loading {
			w.WriteHeader(503)
			return
		}
		switch r.URL.Path {
		case "/ggrun/client":
			json.NewEncoder(w).Encode(p)
		case "/health":
			io.WriteString(w, `{"status":"ok"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestGuidedFirstRunConnectsWaitsInstallsAndLaunches(t *testing.T) {
	home(t)
	fakeClaude(t)
	s := gatewayFixture(t, testToken, 2) // model still loading at first
	g := stubGuided(t, testToken)
	var out bytes.Buffer
	in := bufio.NewReader(strings.NewReader(s.URL + "\n" + "y\n"))
	if err := guidedStart(in, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if c, err := load("default"); err != nil || c.Token != testToken || c.URL != s.URL {
		t.Fatalf("connection not saved: %+v %v", c, err)
	}
	if !strings.Contains(out.String(), "loading the model; waiting") {
		t.Fatalf("did not wait for loading model:\n%s", out.String())
	}
	if len(g.installed) != 1 || g.installed[0] != controlsClaudeVersion {
		t.Fatalf("qualified Claude not offered: %v", g.installed)
	}
	if len(g.launched) != 1 || strings.Join(g.launched[0], " ") != "--connection default" {
		t.Fatalf("launch args: %v", g.launched)
	}
	if !strings.Contains(out.String(), "/goal") {
		t.Fatal("goal command not explained")
	}
}

func TestGuidedRepairsChangedTokenAndOffersResume(t *testing.T) {
	home(t)
	fakeClaude(t)
	newToken := strings.Repeat("n", 40)
	s := gatewayFixture(t, newToken, 0)
	// A saved connection with the old token and a resumable session here.
	c := connection{Version: 1, URL: s.URL, Token: testToken, Profile: profile()}
	b, _ := json.Marshal(c)
	if err := clientkit.WritePrivate(mustConnectionPath("server"), b); err != nil {
		t.Fatal(err)
	}
	sp, _ := sessionPath("server")
	if err := saveSession(sp, session{ID: "11111111-2222-4333-8444-555555555555", ModelID: profile().ModelID, Context: 65536, Version: 1}); err != nil {
		t.Fatal(err)
	}
	g := stubGuided(t, newToken)
	var out bytes.Buffer
	// Answers: install the qualified Claude, then resume.
	in := bufio.NewReader(strings.NewReader("y\nn\ny\n"))
	if err := guidedStart(in, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if c, _ := load("server"); c.Token != newToken {
		t.Fatal("changed token not saved")
	}
	if !strings.Contains(out.String(), "gateway token changed") {
		t.Fatalf("token change not explained:\n%s", out.String())
	}
	if len(g.launched) != 1 || !strings.Contains(strings.Join(g.launched[0], " "), "--connection server") || g.launched[0][len(g.launched[0])-1] != "--resume" {
		t.Fatalf("resume not used: %v", g.launched)
	}
}

func TestGuidedStopsWhenQualifiedClaudeDeclined(t *testing.T) {
	home(t)
	fakeClaude(t)
	s := gatewayFixture(t, testToken, 0)
	g := stubGuided(t, testToken)
	in := bufio.NewReader(strings.NewReader(s.URL + "\nn\n"))
	err := guidedStart(in, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "claude install "+controlsClaudeVersion) || len(g.launched) != 0 {
		t.Fatalf("declined install still launched: %v %v", err, g.launched)
	}
}
