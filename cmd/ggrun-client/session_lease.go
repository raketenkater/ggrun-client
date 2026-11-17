package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errSessionRunning = errors.New("a client is already running for this connection and project")

// The kernel releases the lease even when the launcher crashes. Never unlink
// the lock file: doing so could let another process lock a different inode.
func acquireSessionLease(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".run-lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockSessionFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func requireLiveSession(path string) error {
	f, err := acquireSessionLease(path)
	if errors.Is(err, errSessionRunning) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot verify the running session: %w", err)
	}
	f.Close()
	return fmt.Errorf("no client is running for this connection and project; relaunch with --goal-only --goal-file FILE")
}
