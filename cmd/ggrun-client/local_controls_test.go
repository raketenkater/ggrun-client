package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raketenkater/ggrun/clientkit"
)

func TestControlSettingsPreservePolicyAndUsePortableHooks(t *testing.T) {
	home(t)
	raw := `{"permissions":{"deny":["Bash(rm *)"],"ask":["Bash(git push *)"]},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"my-trusted-hook"}]}]}}`
	args, e := controlArgs([]string{"--settings", raw, "--permission-mode", "auto"}, "fixture", 3*time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	var settings map[string]json.RawMessage
	if json.Unmarshal([]byte(args[1]), &settings) != nil {
		t.Fatal("invalid settings")
	}
	if !strings.Contains(string(settings["permissions"]), "Bash(rm *)") {
		t.Fatal("deny rules lost")
	}
	var hooks map[string][]map[string]any
	if json.Unmarshal(settings["hooks"], &hooks) != nil {
		t.Fatal("invalid hooks")
	}
	if len(hooks["PreToolUse"]) != 2 || len(hooks["Stop"]) != 1 {
		t.Fatal("user hook lost or control hooks missing")
	}
	for _, event := range []string{"PreToolUse", "Stop"} {
		row := hooks[event][len(hooks[event])-1]
		h := row["hooks"].([]any)[0].(map[string]any)
		if h["timeout"] != float64(210) || h["onFailure"] != "block" || !strings.Contains(h["command"].(string), "--goal-state") {
			t.Fatal("hook policy incomplete")
		}
	}
	for _, bad := range []string{"--bare", "--no-session-persistence"} {
		if _, e := controlArgs([]string{bad}, "fixture", time.Minute); e == nil {
			t.Fatal("accepted disabled evidence", bad)
		}
	}
}

func TestGoalSetClearResumePreserveCondition(t *testing.T) {
	home(t)
	sp, e := sessionPath("fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = saveSession(sp, session{ID: "session", ModelID: "model", Context: 262144, Version: 1, Controls: true}); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(t.TempDir(), "GOAL.md")
	if e = os.WriteFile(file, []byte("all independent checks pass"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = goalCommand([]string{"set", "--connection", "fixture", "--file", file}); e != nil {
		t.Fatal(e)
	}
	path, _ := goalFile("fixture")
	g, e := readGoal(path)
	if e != nil || g.State != "active" || g.Session != "session" {
		t.Fatalf("invalid active goal: %v", e)
	}
	revision := g.Revision
	if e = goalCommand([]string{"clear", "--connection", "fixture"}); e != nil {
		t.Fatal(e)
	}
	cleared, _ := readGoal(path)
	if cleared.State != "cleared" || cleared.Revision == revision {
		t.Fatal("clear didn't invalidate evaluator")
	}
	if e = goalCommand([]string{"resume", "--connection", "fixture"}); e != nil {
		t.Fatal(e)
	}
	resumed, _ := readGoal(path)
	if resumed.State != "active" || resumed.Condition != g.Condition {
		t.Fatal("resume changed condition")
	}
}

func goalFixture(t *testing.T, handler http.HandlerFunc) (string, reviewInput) {
	t.Helper()
	home(t)
	p := profile()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ggrun/client":
			json.NewEncoder(w).Encode(p)
		case "/health":
			w.Write([]byte(`{"status":"ok"}`))
		default:
			handler(w, r)
		}
	}))
	t.Cleanup(s.Close)
	cp, e := connectionPath("fixture")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(connection{Version: 1, URL: s.URL, Token: strings.Repeat("x", 32), Profile: p})
	if e = clientkit.WritePrivate(cp, b); e != nil {
		t.Fatal(e)
	}
	path, e := goalFile("fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = withGoalLock(path, func() error {
		return writeGoal(path, goalRecord{Version: 1, Session: "fixture-session", Model: p.ModelID, Condition: "marker created and tests pass", State: "active", Revision: "one"})
	}); e != nil {
		t.Fatal(e)
	}
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if e = os.WriteFile(transcript, []byte(`{"type":"user","message":{"role":"user","content":"complete fixture"}}`+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	return path, reviewInput{Event: "Stop", Session: "fixture-session", CWD: t.TempDir(), Transcript: transcript, LastAssistant: "Work is not finished yet"}
}

func TestGoalRequiresActualEvaluatorBeforeCompleting(t *testing.T) {
	var calls atomic.Int32
	path, input := goalFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["system"] != goalPolicy {
			t.Error("wrong evaluator policy")
		}
		decision := "deny"
		if calls.Add(1) == 2 {
			decision = "allow"
		}
		json.NewEncoder(w).Encode(map[string]any{"stop_reason": "tool_use", "content": []any{map[string]any{"type": "tool_use", "name": "submit_review", "input": map[string]string{"decision": decision, "reason": "fixture evidence"}}}})
	})
	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		if e := localGoalHook("fixture", 5*time.Second, path, input, &out); e != nil {
			t.Fatal(e)
		}
		g, _ := readGoal(path)
		if i == 0 && (g.State != "active" || !strings.Contains(out.String(), `"block"`)) {
			t.Fatal("unmet goal did not continue")
		}
		if i == 1 && (g.State != "complete" || strings.Contains(out.String(), `"block"`)) {
			t.Fatal("met goal did not finish")
		}
	}
	var out bytes.Buffer
	if e := localGoalHook("fixture", time.Second, path, input, &out); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal("completed goal evaluated again")
	}
}

func TestGoalFailurePausesInsteadOfCompleting(t *testing.T) {
	path, input := goalFixture(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unavailable", 503) })
	var out bytes.Buffer
	if e := localGoalHook("fixture", time.Second, path, input, &out); e == nil {
		t.Fatal("failure hidden")
	}
	g, _ := readGoal(path)
	if g.State != "paused" || g.Reason == "" {
		t.Fatal("failed evaluator completed or lost condition")
	}
}

func TestClearCancelsGoalWithoutRevivingState(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	path, input := goalFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var b any
		json.NewDecoder(r.Body).Decode(&b)
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	done := make(chan error, 1)
	go func() { var out bytes.Buffer; done <- localGoalHook("fixture", 10*time.Second, path, input, &out) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("evaluator did not start")
	}
	if e := goalCommand([]string{"clear", "--connection", "fixture"}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream not cancelled")
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("hook did not finish")
	}
	g, _ := readGoal(path)
	if g.State != "cleared" {
		t.Fatal("late result revived goal")
	}
}
