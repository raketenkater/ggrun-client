package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStatusLineSeparatesSlowLostAndUnreachable(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	zero, one := 0, 1
	progress := func(line string, inFlight *int) *serverProgress {
		p := &serverProgress{InFlight: inFlight}
		p.Progress = &struct {
			Line string `json:"line"`
		}{line}
		return p
	}
	waiting := &relayState{Qualified: true, InFlight: []relayRequest{{Started: now.Add(-3 * time.Minute)}}}
	for _, tc := range []struct {
		name     string
		sp       *serverProgress
		rt       *routerStatus
		err      error
		rs       *relayState
		path     string
		want     []string
		unwanted []string
	}{
		{name: "server speeds pass through", sp: progress("ggrun · S0 prefill 12,000/~90,000 · prefill 130.0 tok/s · decode 4.8 tok/s · 1m32s", &one), rs: waiting,
			want: []string{"prefill 130.0 tok/s", "decode 4.8 tok/s"}, unwanted: []string{"⚠"}},
		{name: "idle server says server not local", sp: progress("ggrun · local ready · 1 slots", &zero), want: []string{"ggrun · server ready"}},
		{name: "request lost between machines", sp: progress("ggrun · server ready", &zero), rs: waiting, want: []string{"⚠ request not reaching server · 3m00s"}},
		{name: "young request is not yet lost", sp: progress("ggrun · server ready", &zero),
			rs: &relayState{Qualified: true, InFlight: []relayRequest{{Started: now.Add(-5 * time.Second)}}}, unwanted: []string{"⚠"}},
		{name: "server unreachable", err: errors.New("dial"), rs: waiting, want: []string{"server unreachable", "waiting 3m00s"}},
		{name: "stream went silent", sp: progress("ggrun · S0 generating 12 tok", &one),
			rs:   &relayState{Qualified: true, InFlight: []relayRequest{{Started: now.Add(-10 * time.Minute), Headers: now.Add(-9 * time.Minute), LastByte: now.Add(-4 * time.Minute)}}},
			want: []string{"⚠ no data for 4m00s"}},
		{name: "retrying after a lost connection", sp: progress("ggrun · server ready", &zero),
			rs: &relayState{Qualified: true, ConsecutiveErr: 2, LastErrorAt: now.Add(-10 * time.Second)}, want: []string{"connection lost · Claude retries (2)"}},
		{name: "old server falls back to router counts", rt: &routerStatus{Active: 1, Queued: 2, Limit: 1}, want: []string{"active 1/1 · queued 2"}},
		{name: "untested and changed Claude", sp: progress("ggrun · server ready", &zero),
			rs: &relayState{ClaudeVersion: "2.1.319", ClaudePath: "/v/2.1.316"}, path: "/v/2.1.319",
			want: []string{"untested Claude 2.1.319", "Claude changed on disk"}},
	} {
		got := statusLine(tc.sp, tc.rt, tc.err, tc.rs, now, tc.path)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", tc.name, got, w)
			}
		}
		for _, u := range tc.unwanted {
			if strings.Contains(got, u) {
				t.Errorf("%s: %q contains %q", tc.name, got, u)
			}
		}
	}
}
