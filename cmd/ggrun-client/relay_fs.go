package main

import (
	"os"
	"runtime"
	"time"
)

// retryTransientFS retries op while Windows reports a sharing violation or
// access denied. Replacing a file there fails while another process holds it
// open, and opening it fails during the replacement. Other platforms and
// other errors return immediately. The wait is bounded to about 1s.
func retryTransientFS(op func() error) error {
	err := op()
	if runtime.GOOS != "windows" {
		return err
	}
	for i := 0; i < 100 && err != nil && !os.IsNotExist(err); i++ {
		time.Sleep(10 * time.Millisecond)
		err = op()
	}
	return err
}
