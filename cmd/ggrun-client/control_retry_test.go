package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestControlRetriesReadOnlyFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(503)
		case 2:
			fmt.Fprint(w, `{"stop_reason":"max_tokens","content":[]}`)
		default:
			fmt.Fprint(w, `{"stop_reason":"tool_use","content":[{"type":"tool_use","name":"submit_review","input":{"decision":"deny","reason":"work remains"}}]}`)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v, err := evaluateControl(ctx, connection{URL: server.URL, Profile: profile()}, []byte("evidence"), goalPolicy)
	if err != nil || v.Decision != "deny" || calls.Load() != 3 {
		t.Fatalf("bounded retry: %+v %v calls=%d", v, err, calls.Load())
	}
}

func TestControlDoesNotRetryPermanentFailureOrVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, decision string
		code           int
	}{
		{"authentication", "", 401}, {"context", "", 400}, {"unsupported", "", 501},
		{"deny", "deny", 200}, {"ask", "ask", 200}, {"allow", "allow", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.code)
				fmt.Fprintf(w, `{"stop_reason":"tool_use","content":[{"type":"tool_use","name":"submit_review","input":{"decision":%q,"reason":"evidence"}}]}`, tc.decision)
			}))
			defer server.Close()
			v, err := evaluateControl(context.Background(), connection{URL: server.URL, Profile: profile()}, []byte("evidence"), goalPolicy)
			if calls.Load() != 1 || (err != nil) != (tc.code != 200) || v.Decision != tc.decision {
				t.Fatalf("unexpected outcome: %+v %v %d", v, err, calls.Load())
			}
			if err != nil && !permanentGoalError(err) {
				t.Fatal("permanent error would repeatedly continue the goal")
			}
		})
	}
}

func TestControlRetryBudgetAndCancellation(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		v, err := retryControl(ctx, func() (reviewVerdict, error) {
			calls++
			if !exhausted {
				cancel()
			}
			return reviewVerdict{}, &controlFailure{kind: "invalid_verdict", retry: true, cause: errors.New("missing decision")}
		})
		cancel()
		want := 1
		if exhausted {
			want = 3
		}
		if err == nil || v.Decision != "" || calls != want {
			t.Fatalf("retry budget: %+v %v %d", v, err, calls)
		}
	}
}

func TestControlInspectionRespectsHookCancellation(t *testing.T) {
	var calls atomic.Int32
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer gw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := inspectContext(ctx, connection{URL: gw.URL})
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("canceled hook still inspects gateway: %v", err)
	}
	if !permanentGoalError(errCredential) {
		t.Fatal("authentication error treated as transient")
	}
}
