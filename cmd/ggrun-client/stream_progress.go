package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// No startup deadline by default: without request-correlated backend counters,
// silence during queueing/prefill is not evidence of a stalled model.
type relayPolicy struct{ GenerationIdle, Startup time.Duration }

func relayPolicyFromEnv() (relayPolicy, error) {
	p := relayPolicy{GenerationIdle: 3 * time.Minute}
	for _, item := range []struct {
		name string
		dst  *time.Duration
	}{
		{"GGRUN_GENERATION_IDLE_TIMEOUT", &p.GenerationIdle},
		{"GGRUN_REQUEST_STARTUP_TIMEOUT", &p.Startup},
	} {
		if v := os.Getenv(item.name); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d < 0 || d > 24*time.Hour {
				return p, fmt.Errorf("%s must be a duration between 0 and 24h", item.name)
			}
			*item.dst = d
		}
	}
	return p, nil
}

// sseProgress observes only event metadata. Payload buffers are bounded and
// discarded after each event, and are never persisted or logged.
type sseProgress struct {
	line, data         []byte
	oversized          bool
	generated, stopped bool
	failed             bool
}

const maxProgressEvent = 256 << 10

func (s *sseProgress) feed(p []byte) bool {
	progress := false
	for _, c := range p {
		if c != '\n' {
			if len(s.line) < maxProgressEvent {
				s.line = append(s.line, c)
			} else {
				s.oversized = true
			}
			continue
		}
		line := bytes.TrimSuffix(s.line, []byte{'\r'})
		if len(line) == 0 {
			if !s.oversized && s.event() {
				progress = true
			}
			s.data = s.data[:0]
			s.oversized = false
		} else if bytes.HasPrefix(line, []byte("data:")) {
			v := bytes.TrimPrefix(line, []byte("data:"))
			v = bytes.TrimPrefix(v, []byte(" "))
			if len(s.data)+len(v)+1 <= maxProgressEvent {
				s.data = append(s.data, v...)
				s.data = append(s.data, '\n')
			} else {
				s.oversized = true
			}
		}
		s.line = s.line[:0]
	}
	return progress
}

func (s *sseProgress) event() bool {
	if len(s.data) == 0 {
		return false
	}
	var v struct {
		Type  string `json:"type"`
		Delta struct {
			Type, Text, Thinking string
			PartialJSON          string `json:"partial_json"`
		} `json:"delta"`
		Block struct {
			Type string `json:"type"`
		} `json:"content_block"`
	}
	if json.Unmarshal(s.data, &v) != nil {
		s.failed = true
		return false
	}
	switch v.Type {
	case "error":
		s.failed = true
	case "message_stop":
		s.stopped = true
	case "content_block_start":
		// An empty block start identifies generation, but is not token progress.
		if v.Block.Type == "text" || v.Block.Type == "thinking" || v.Block.Type == "tool_use" {
			s.generated = true
		}
	case "content_block_delta":
		d := v.Delta
		if (d.Type == "text_delta" && d.Text != "") || (d.Type == "thinking_delta" && d.Thinking != "") || (d.Type == "input_json_delta" && d.PartialJSON != "") {
			s.generated = true
			return true
		}
	}
	return false
}

func streamContentType(v string) bool {
	return strings.EqualFold(strings.TrimSpace(strings.Split(v, ";")[0]), "text/event-stream")
}
