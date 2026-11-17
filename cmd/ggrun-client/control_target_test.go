package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/raketenkater/ggrun/clientkit"
)

func TestExplicitUtilityUsesPublishedContextAndKeepsGatewayIdentity(t *testing.T) {
	p := profile()
	if _, err := controlTargetFor(p, "utility"); err == nil {
		t.Fatal("guessed legacy utility context")
	}
	p.UtilityRoute = &clientkit.UtilityRoute{ContextPerSlot: 8192, Slots: 1, SeparateBackend: true, FallbackToMain: true}
	target, err := controlTargetFor(p, "utility")
	if err != nil || target.context != 8192 || !target.fallback {
		t.Fatalf("target: %+v %v", target, err)
	}
	var calls atomic.Int32
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct{ Model string }
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model != p.UtilityAlias || r.Header.Get("X-Ggrun-Model-ID") != p.ModelID || r.Header.Get("X-Ggrun-Context") != "65536" {
			t.Error("routing changed gateway session contract")
		}
		w.Write([]byte(`{"stop_reason":"tool_use","content":[{"type":"tool_use","name":"submit_review","input":{"decision":"ask","reason":"need evidence"}}]}`))
	}))
	defer gw.Close()
	c := connection{URL: gw.URL, Profile: p}
	v, err := evaluateControlTarget(context.Background(), c, []byte("fixture"), goalPolicy, target)
	if err != nil || v.Decision != "ask" {
		t.Fatalf("utility verdict: %+v %v", v, err)
	}
	_, err = evaluateControlTarget(context.Background(), c, []byte(strings.Repeat("x", 32768)), goalPolicy, target)
	if err == nil || calls.Load() != 1 {
		t.Fatal("oversized evidence was sent or silently truncated")
	}
}

func TestControlSelectionsAreIndependentAndOptIn(t *testing.T) {
	home(t)
	p := profile()
	p.UtilityRoute = &clientkit.UtilityRoute{ContextPerSlot: 8192, Slots: 1, SeparateBackend: true, FallbackToMain: true}
	for _, goal := range []bool{true, false} {
		target, err := selectedControlTarget("fixture", p, goal)
		if err != nil || target.alias != p.MainAlias {
			t.Fatal("helper enabled without selection")
		}
	}
	path, _ := customizationPath("fixture")
	b, _ := json.Marshal(customization{GoalEvaluator: "utility"})
	if err := clientkit.ReplacePrivate(path, b); err != nil {
		t.Fatal(err)
	}
	goal, err := selectedControlTarget("fixture", p, true)
	if err != nil || goal.alias != p.UtilityAlias {
		t.Fatal("selected goal evaluator ignored")
	}
	review, err := selectedControlTarget("fixture", p, false)
	if err != nil || review.alias != p.MainAlias {
		t.Fatal("goal selection changed tool reviewer")
	}
}
