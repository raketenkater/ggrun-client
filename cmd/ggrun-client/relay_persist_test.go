package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentRelaySnapshotsStayComplete(t *testing.T) {
	r := &relay{path: filepath.Join(t.TempDir(), "relay.json"), active: map[int]*relayRequest{}}
	r.state.LastError = strings.Repeat("transport failure ", 4096)
	r.persist()
	var writers sync.WaitGroup
	for i := 0; i < 8; i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for j := 0; j < 30; j++ {
				r.persist()
			}
		}()
	}
	done := make(chan struct{})
	go func() { writers.Wait(); close(done) }()
	for {
		select {
		case <-done:
			r.mu.Lock()
			r.state.Completed = 1
			r.state.LastOutcome = "complete"
			r.mu.Unlock()
			r.persist()
			s, err := readRelayState(r.path)
			if err != nil || s.Completed != 1 || s.LastOutcome != "complete" {
				t.Fatalf("final status lost: %+v %v", s, err)
			}
			return
		default:
			s, err := readRelayState(r.path)
			if err != nil && !os.IsNotExist(err) {
				t.Errorf("reader observed a partial snapshot: %v", err)
				<-done
				return
			}
			if err == nil && s.LastError != r.state.LastError {
				t.Error("reader observed truncated status")
				<-done
				return
			}
		}
	}
}
