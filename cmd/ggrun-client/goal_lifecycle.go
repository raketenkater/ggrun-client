package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/raketenkater/ggrun/clientkit"
)

func firstRunID(ids []string) string {
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

func verifyGoalOwner(path string, g goalRecord) error {
	b, err := clientkit.ReadPrivate(path)
	if err != nil {
		return fmt.Errorf("cannot read verified session; relaunch with the goal file")
	}
	var s session
	if json.Unmarshal(b, &s) != nil || !s.Goals || s.ID != g.Session || s.ModelID != g.Model || s.RunID != g.RunID {
		return fmt.Errorf("goal belongs to another launch or no client goal hook is installed; relaunch with --goal-only --goal-file FILE")
	}
	return requireLiveSession(path)
}

func pauseGoal(path, sessionID, runID, reason string) error {
	if path == "" || sessionID == "" {
		return fmt.Errorf("goal pause requires a verified state path and session")
	}
	return withGoalLock(path, func() error {
		g, err := readGoal(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if g.Session != sessionID || (runID != "" && g.RunID != runID) || g.State != "active" {
			return nil
		}
		g.State, g.Reason = "paused", reason
		g.Revision = fmt.Sprint(time.Now().UnixNano())
		return writeGoal(path, g)
	})
}
