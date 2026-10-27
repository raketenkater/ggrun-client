package main

import (
	"os"
	"os/signal"
)

// A foreground Claude process receives terminal signals itself. Keep this
// wrapper alive until it exits; no client signal is sent to the server.
func holdInterrupt() func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	return func() { signal.Stop(ch) }
}
