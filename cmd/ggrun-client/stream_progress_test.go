package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSemanticProgressFragmented(t *testing.T) {
	for _, delta := range []string{`{"type":"text_delta","text":"x"}`, `{"type":"thinking_delta","thinking":"x"}`, `{"type":"input_json_delta","partial_json":"{"}`} {
		var p sseProgress
		wire := "event: content_block_delta\r\ndata: {\"type\":\"content_block_delta\",\r\ndata: \"delta\":" + delta + "}\r\n\r\n"
		count := 0
		for i := range len(wire) {
			if p.feed([]byte(wire[i : i+1])) {
				count++
			}
		}
		if count != 1 || !p.generated || p.failed {
			t.Fatalf("fragmented event: %+v", p)
		}
	}
	var p sseProgress
	if p.feed([]byte(": ping\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"\"}}\n\n")) || p.generated {
		t.Fatal("heartbeat or empty delta counted as output")
	}
	p.feed([]byte("data: " + strings.Repeat("x", maxProgressEvent*2) + "\n\n"))
	if len(p.line) > maxProgressEvent || len(p.data) > maxProgressEvent {
		t.Fatal("unbounded parser")
	}
	p.feed([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	if !p.stopped {
		t.Fatal("parser failed to recover after oversized event")
	}
}

const generationStart = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\"}}\n\n"
const textDelta = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n"
const messageStop = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func TestRelayGenerationStallCancelsUpstream(t *testing.T) {
	t.Setenv("GGRUN_GENERATION_IDLE_TIMEOUT", "80ms")
	released := make(chan struct{})
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(released)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, generationStart)
		w.(http.Flusher).Flush()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer gw.Close()
	rl, err := startRelay(gw.URL, "", relayState{})
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()
	cl := &http.Client{Timeout: time.Second}
	resp, err := cl.Post(rl.URL()+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if err == nil {
		t.Fatal("stall presented as clean stream EOF")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("upstream not canceled")
	}
	s := rl.snapshot()
	if s.Completed != 0 || s.LastOutcome != "failed" || !strings.Contains(s.LastError, "progress budget") {
		t.Fatalf("stall outcome: %+v", s)
	}
}

func TestRelaySlowPrefillAndSteadyGeneration(t *testing.T) {
	t.Setenv("GGRUN_GENERATION_IDLE_TIMEOUT", "100ms")
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(250 * time.Millisecond) // unknown prefill exceeds generation budget
		io.WriteString(w, generationStart)
		for range 8 {
			io.WriteString(w, textDelta)
			w.(http.Flusher).Flush()
			time.Sleep(30 * time.Millisecond)
		}
		io.WriteString(w, messageStop)
	}))
	defer gw.Close()
	rl, err := startRelay(gw.URL, "", relayState{})
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()
	cl := &http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Post(rl.URL()+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	want := "event: ping\ndata: {\"type\":\"ping\"}\n\n" + generationStart + strings.Repeat(textDelta, 8) + messageStop
	if err != nil || string(b) != want {
		t.Fatalf("stream changed or aborted: %v", err)
	}
	if s := rl.snapshot(); s.Completed != 1 || s.ConsecutiveErr != 0 {
		t.Fatalf("healthy stream: %+v", s)
	}
}

func TestRelayTerminalOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, body      string
		status          int
		stream, success bool
	}{
		{"truncated", textDelta, 200, true, false},
		{"api error", "data: {\"type\":\"error\"}\n\n" + messageStop, 200, true, false},
		{"http error", `{"error":"no"}`, 503, false, false},
		{"nonstream", `{"content":[]}`, 200, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer gw.Close()
			rl, err := startRelay(gw.URL, "", relayState{})
			if err != nil {
				t.Fatal(err)
			}
			defer rl.Close()
			resp, err := http.Get(rl.URL())
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			s := rl.snapshot()
			if (s.Completed == 1) != tc.success || (s.ConsecutiveErr == 0) != tc.success {
				t.Fatalf("outcome: %+v", s)
			}
		})
	}
}

func TestRelayStartupBudgetAndCancellation(t *testing.T) {
	t.Setenv("GGRUN_REQUEST_STARTUP_TIMEOUT", "60ms")
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer gw.Close()
	rl, err := startRelay(gw.URL, "", relayState{})
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()
	resp, err := http.Get(rl.URL())
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 502 || !strings.Contains(rl.snapshot().LastError, "unknown progress budget") {
		t.Fatalf("startup: %+v", rl.snapshot())
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", rl.URL(), nil)
	cancel()
	http.DefaultClient.Do(req)
}

func TestRelayPolicyValidation(t *testing.T) {
	for _, v := range []string{"-1s", "no", "25h"} {
		t.Setenv("GGRUN_GENERATION_IDLE_TIMEOUT", v)
		if _, err := relayPolicyFromEnv(); err == nil {
			t.Fatalf("accepted %q", v)
		}
	}
	t.Setenv("GGRUN_GENERATION_IDLE_TIMEOUT", "0")
	p, err := relayPolicyFromEnv()
	if err != nil || p.GenerationIdle != 0 {
		t.Fatal("explicit opt-out rejected")
	}
}

func TestRelayUserCancellationReleasesUpstream(t *testing.T) {
	arrived, released := make(chan struct{}), make(chan struct{})
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-r.Context().Done()
		close(released)
	}))
	defer gw.Close()
	rl, err := startRelay(gw.URL, "", relayState{})
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", rl.URL(), nil)
	done := make(chan struct{})
	go func() {
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		close(done)
	}()
	select {
	case <-arrived:
	case <-time.After(time.Second):
		t.Fatal("request not delivered")
	}
	cancel()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("backend request not canceled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("client remained stuck")
	}
	deadline := time.Now().Add(time.Second)
	for len(rl.snapshot().InFlight) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s := rl.snapshot(); s.Completed != 0 || s.Canceled != 1 || s.ConsecutiveErr != 0 {
		t.Fatalf("user cancellation counted incorrectly: %+v", s)
	}
}
