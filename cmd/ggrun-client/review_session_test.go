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

func TestReviewUsesVerifiedSessionInsteadOfPairingSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, decision                                    string
		changeModel, shrink, wrongSession, missing, moved bool
	}{
		{name: "stale pairing snapshot", decision: "allow"},
		{name: "preserve deny", decision: "deny"},
		{name: "preserve ask", decision: "ask"},
		{name: "directory changed within session", decision: "allow", moved: true},
		{name: "actual model changed", changeModel: true},
		{name: "actual context reduced", shrink: true},
		{name: "wrong native session", wrongSession: true},
		{name: "missing session", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home(t)
			p := profile()
			project := t.TempDir()
			s := session{ID: "active-native-session", ModelID: p.ModelID, Context: p.ContextPerSlot, Version: 1, Controls: true, Dir: project}
			sp, err := sessionPathForDir("fixture", project)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.missing {
				if err = saveSession(sp, s); err != nil {
					t.Fatal(err)
				}
			}
			if tc.changeModel {
				p.ModelID = "different-live-model"
			}
			if tc.shrink {
				p.ContextPerSlot /= 2
				p.Policy.CompactWindow = p.ContextPerSlot
			}
			var reviews atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/ggrun/client":
					json.NewEncoder(w).Encode(p)
				case "/health":
					w.Write([]byte(`{"status":"ok"}`))
				case "/v1/messages":
					reviews.Add(1)
					if r.Header.Get("X-Ggrun-Model-ID") != p.ModelID {
						t.Error("review used stale identity")
					}
					json.NewEncoder(w).Encode(map[string]any{"stop_reason": "tool_use", "content": []any{map[string]any{"type": "tool_use", "name": "submit_review", "input": map[string]string{"decision": tc.decision, "reason": "fixture verdict"}}}})
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			old := profile()
			old.ModelID = "old-pairing-model"
			old.ContextPerSlot = 262144
			old.Policy.CompactWindow = 262144
			cp, _ := connectionPath("fixture")
			data, _ := json.Marshal(connection{Version: 1, URL: server.URL, Token: testToken, Profile: old})
			if err = clientkit.WritePrivate(cp, data); err != nil {
				t.Fatal(err)
			}
			transcript := filepath.Join(project, "transcript.jsonl")
			if err = os.WriteFile(transcript, []byte(`{"type":"user","message":{"role":"user","content":"Inspect the current project directory."}}`+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			input := reviewInput{Event: "PreToolUse", Session: s.ID, CWD: project, Transcript: transcript, Mode: "auto", Tool: "Bash", Input: json.RawMessage(`{"command":"pwd"}`)}
			if tc.wrongSession {
				input.Session = "different-session"
			}
			if tc.moved {
				input.CWD = t.TempDir()
			}
			b, _ := json.Marshal(input)
			var out bytes.Buffer
			if tc.moved {
				err = localControlHook("fixture", 5*time.Second, sp+".goal", bytes.NewReader(b), &out)
			} else {
				err = mainReviewHook("fixture", 5*time.Second, bytes.NewReader(b), &out)
			}
			if tc.decision == "" {
				if err == nil || reviews.Load() != 0 || strings.Contains(out.String(), "allow") {
					t.Fatalf("invalid session reached review: err=%v calls=%d out=%s", err, reviews.Load(), out.String())
				}
			} else {
				if err != nil || reviews.Load() != 1 || !strings.Contains(out.String(), `"permissionDecision":"`+tc.decision+`"`) {
					t.Fatalf("valid session failed: err=%v calls=%d out=%s", err, reviews.Load(), out.String())
				}
			}
		})
	}
}
