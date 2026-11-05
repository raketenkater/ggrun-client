package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewRejectsIncompleteOrAmbiguousVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name, text, stop string
		valid            bool
	}{
		{"allow", `{"decision":"allow","reason":"Exact user authorization"}`, "end_turn", true},
		{"deny", `{"decision":"deny","reason":"Outside scope"}`, "end_turn", true},
		{"ask", `{"decision":"ask","reason":"Need target scope"}`, "end_turn", true},
		{"empty", "", "end_turn", false},
		{"missing reason", `{"decision":"allow"}`, "end_turn", false},
		{"truncated", `{"decision":"allow","reason":"authorized"}`, "max_tokens", false},
		{"duplicate", `{"decision":"deny","decision":"allow","reason":"x"}`, "end_turn", false},
		{"trailing", `{"decision":"allow","reason":"x"} {}`, "end_turn", false},
		{"extra", `{"decision":"allow","reason":"x","extra":true}`, "end_turn", false},
		{"boolean", `{"decision":true,"reason":"x"}`, "end_turn", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing authentication")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid body")
				}
				if body["model"] != "local" {
					t.Error("review must select main alias")
				}
				json.NewEncoder(w).Encode(map[string]any{"stop_reason": tc.stop, "content": []any{map[string]any{"type": "text", "text": tc.text}}})
			}))
			defer s.Close()
			c := connection{URL: s.URL, Token: "fixture"}
			c.Profile.MainAlias = "local"
			_, err := evaluateReview(context.Background(), c, []byte(`{"fixture":true}`))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestReviewCancellationAndRedirectDoNotApprove(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		cancelled := make(chan struct{})
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var v any
			_ = json.NewDecoder(r.Body).Decode(&v)
			<-r.Context().Done()
			close(cancelled)
		}))
		defer s.Close()
		c := connection{URL: s.URL}
		c.Profile.MainAlias = "local"
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, err := evaluateReview(ctx, c, []byte(`{}`)); err == nil {
			t.Fatal("cancelled review approved")
		}
		select {
		case <-cancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("upstream not cancelled")
		}
	})
	t.Run("redirect", func(t *testing.T) {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("credential followed redirect") }))
		defer target.Close()
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
		defer s.Close()
		c := connection{URL: s.URL, Token: "private-fixture"}
		c.Profile.MainAlias = "local"
		_, err := evaluateReview(context.Background(), c, []byte(`{}`))
		if err == nil || strings.Contains(err.Error(), c.Token) {
			t.Fatal("redirect approved or credential leaked")
		}
	})
}

func TestReviewThinkingIsNotAVerdict(t *testing.T) {
	for _, blocks := range [][]any{
		{map[string]any{"type": "thinking", "thinking": "private reasoning"}, map[string]any{"type": "text", "text": `{"decision":"deny","reason":"outside authorization"}`}},
		{map[string]any{"type": "thinking", "thinking": `{"decision":"allow","reason":"not a final answer"}`}},
		{map[string]any{"type": "tool_use"}, map[string]any{"type": "text", "text": `{"decision":"allow","reason":"x"}`}},
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			thinking, ok := body["thinking"].(map[string]any)
			if !ok || thinking["type"] != "disabled" {
				t.Error("control reasoning must be disabled")
			}
			json.NewEncoder(w).Encode(map[string]any{"stop_reason": "end_turn", "content": blocks})
		}))
		c := connection{URL: s.URL}
		c.Profile.MainAlias = "local"
		v, err := evaluateReview(context.Background(), c, []byte(`{}`))
		s.Close()
		valid := len(blocks) == 2 && blocks[0].(map[string]any)["type"] == "thinking"
		if valid && (err != nil || v.Decision != "deny") {
			t.Fatalf("final denial not retained: %v", err)
		}
		if !valid && err == nil {
			t.Fatal("non-final or tool-bearing response accepted")
		}
	}
}

func TestReviewStructuredVerdictDoesNotExecuteTools(t *testing.T) {
	for _, name := range []string{"submit_review", "Bash"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			choice, ok := body["tool_choice"].(map[string]any)
			if !ok || choice["name"] != "submit_review" || choice["type"] != "tool" {
				t.Error("structured verdict not requested")
			}
			json.NewEncoder(w).Encode(map[string]any{"stop_reason": "tool_use", "content": []any{
				map[string]any{"type": "text", "text": `{"decision":"allow","reason":"non-authoritative commentary"}`},
				map[string]any{"type": "tool_use", "name": name, "input": map[string]string{"decision": "deny", "reason": "not authorized"}},
			}})
		}))
		c := connection{URL: s.URL}
		c.Profile.MainAlias = "local"
		v, err := evaluateReview(context.Background(), c, []byte(`{}`))
		s.Close()
		if name == "submit_review" && (err != nil || v.Decision != "deny") {
			t.Fatalf("valid structured denial rejected: %v", err)
		}
		if name != "submit_review" && err == nil {
			t.Fatal("unexpected model tool accepted")
		}
	}
}

func TestReviewEvidenceIsCompleteAndRequired(t *testing.T) {
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	text := `{"type":"user","message":{"role":"user","content":"Run tests"}}` + "\n"
	if err := os.WriteFile(p, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	in := reviewInput{Event: "PreToolUse", Session: "fixture", Tool: "Bash", Input: json.RawMessage(`{"command":"go test ./..."}`), CWD: t.TempDir(), Transcript: p}
	b, err := readReviewEvidence(in)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Transcript string      `json:"native_transcript_jsonl"`
		Action     reviewInput `json:"proposed_action"`
	}
	if json.Unmarshal(b, &envelope) != nil || envelope.Transcript != text || string(envelope.Action.Input) != string(in.Input) {
		t.Fatal("evidence altered")
	}
	in.Transcript = ""
	if _, err := readReviewEvidence(in); err == nil {
		t.Fatal("missing evidence accepted")
	}
	in.Transcript = p
	if err := os.WriteFile(p, []byte(`{"type":"user"`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReviewEvidence(in); err != errTranscriptPending {
		t.Fatal("partial native journal accepted")
	}
}
