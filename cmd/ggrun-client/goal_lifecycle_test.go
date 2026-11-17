package main

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionLeaseProcessHelper(t *testing.T) {
	path := os.Getenv("GGRUN_TEST_SESSION_LEASE")
	if path == "" {
		return
	}
	f, err := acquireSessionLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Stdout.WriteString("leased\n")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestSessionLeaseReleasedOnCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSessionLeaseProcessHelper$")
	cmd.Env = append(os.Environ(), "GGRUN_TEST_SESSION_LEASE="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "leased\n" {
		t.Fatalf("child did not acquire lease: %q %v", line, err)
	}
	if f, err := acquireSessionLease(path); !errors.Is(err, errSessionRunning) {
		if f != nil {
			f.Close()
		}
		t.Fatalf("concurrent launch accepted: %v", err)
	}
	if err = requireLiveSession(path); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if err = requireLiveSession(path); err == nil {
		t.Fatal("crashed launcher still live")
	}
	f, err := acquireSessionLease(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestGoalCannotResumeDeadLaunch(t *testing.T) {
	home(t)
	sp, _ := sessionPath("fixture")
	s := session{ID: "session", RunID: "run", ModelID: "model", Goals: true, Version: 1}
	if err := saveSession(sp, s); err != nil {
		t.Fatal(err)
	}
	g := goalRecord{Version: 1, Session: s.ID, RunID: s.RunID, Model: s.ModelID, Condition: "test", State: "active", Revision: "one"}
	if err := writeGoal(sp+".goal", g); err != nil {
		t.Fatal(err)
	}
	if err := goalCommand([]string{"resume", "--connection", "fixture"}); err == nil {
		t.Fatal("resumed without running launcher")
	}
	if err := goalCommand([]string{"status", "--connection", "fixture"}); err != nil {
		t.Fatal(err)
	}
	g, _ = readGoal(sp + ".goal")
	if g.State != "paused" || !strings.Contains(g.Reason, "no client is running") {
		t.Fatalf("stale active goal: %+v", g)
	}
}

func TestStopFailurePausesOnlyMatchingLaunch(t *testing.T) {
	home(t)
	path, _ := goalFile("fixture")
	g := goalRecord{Version: 1, Session: "session", RunID: "new", Model: "model", State: "active", Revision: "one"}
	if err := writeGoal(path, g); err != nil {
		t.Fatal(err)
	}
	event := `{"hook_event_name":"StopFailure","session_id":"session","error":"server_error"}`
	for _, run := range []string{"old", "new"} {
		var out bytes.Buffer
		if err := localControlHook("fixture", time.Minute, path, strings.NewReader(event), &out, run); err != nil {
			t.Fatal(err)
		}
		g, _ = readGoal(path)
		if (g.State == "paused") != (run == "new") {
			t.Fatalf("wrong launch updated: %+v", g)
		}
		if strings.TrimSpace(out.String()) != "{}" {
			t.Fatal("StopFailure pretended to control continuation")
		}
	}
}

func TestGoalPausePreservesTerminalAndReplacementStates(t *testing.T) {
	home(t)
	path, _ := goalFile("fixture")
	for _, state := range []string{"complete", "cleared", "paused", "active"} {
		g := goalRecord{Version: 1, Session: "session", RunID: "new", Model: "model", State: state, Revision: "one"}
		if err := writeGoal(path, g); err != nil {
			t.Fatal(err)
		}
		if err := pauseGoal(path, "session", "old", "old exit"); err != nil {
			t.Fatal(err)
		}
		got, _ := readGoal(path)
		if got != g {
			t.Fatal("old launch changed replacement goal")
		}
		if err := pauseGoal(path, "session", "new", "exit"); err != nil {
			t.Fatal(err)
		}
		got, _ = readGoal(path)
		if state == "active" {
			if got.State != "paused" || got.Revision == g.Revision {
				t.Fatal("active evaluator not invalidated")
			}
		} else if got != g {
			t.Fatal("terminal state overwritten on exit")
		}
	}
}
