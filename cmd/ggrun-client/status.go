package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type serverProgress struct {
	InFlight    *int      `json:"messages_in_flight"`
	LastRequest time.Time `json:"last_request_at"`
	Progress    *struct {
		Line string `json:"line"`
	} `json:"progress"`
}

type routerStatus struct{ Active, Queued, Limit int }

// Waiting this long for response headers while the server reports no request
// at all means the request is lost between the two machines.
const lostAfter = 20 * time.Second

func status(args []string) error {
	// Best-effort status: bounded, authenticated, never runs a model probe.
	var name, statePath string
	rest := args
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--relay-state" && i+1 < len(rest) {
			statePath = rest[i+1]
			rest = append(append([]string{}, rest[:i]...), rest[i+2:]...)
			break
		}
	}
	name, err := nameFlag("status", rest)
	if err != nil {
		return err
	}
	c, err := load(name)
	if err != nil {
		fmt.Println("ggrun: reconnect needed")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var sp *serverProgress
	var rt *routerStatus
	var p serverProgress
	code, reqErr := statusGet(ctx, c, "/ggrun/progress", &p)
	if reqErr == nil && code == 200 {
		sp = &p
	} else if reqErr == nil && code == 404 {
		var r routerStatus
		if code, reqErr = statusGet(ctx, c, "/ggrun/router", &r); reqErr == nil && code == 200 {
			rt = &r
		}
	}
	if reqErr == nil && code != 200 {
		reqErr = fmt.Errorf("HTTP %d", code)
	}
	var rs *relayState
	if statePath != "" {
		if s, err := readRelayState(statePath); err == nil {
			rs = &s
		}
	}
	current := ""
	if path, err := exec.LookPath("claude"); err == nil {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			current = resolved
		}
	}
	fmt.Println(statusLine(sp, rt, reqErr, rs, time.Now(), current))
	return nil
}

func statusGet(ctx context.Context, c connection, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.URL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	cl := &http.Client{Transport: remoteTransport(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
			return 0, err
		}
	}
	return resp.StatusCode, nil
}

func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func statusLine(sp *serverProgress, rt *routerStatus, reqErr error, rs *relayState, now time.Time, claudePath string) string {
	var parts []string
	switch {
	case reqErr != nil:
		parts = append(parts, "ggrun · server unreachable")
	case sp != nil && sp.Progress != nil && sp.Progress.Line != "":
		line := sp.Progress.Line
		line = strings.Replace(line, "ggrun · local ", "ggrun · server ", 1)
		parts = append(parts, line)
	case rt != nil:
		parts = append(parts, fmt.Sprintf("ggrun · active %d/%d · queued %d", rt.Active, rt.Limit, rt.Queued))
	default:
		parts = append(parts, "ggrun · connected")
	}
	if rs == nil {
		return strings.Join(parts, " · ")
	}
	if len(rs.InFlight) > 0 {
		q := rs.InFlight[0]
		age := now.Sub(q.Started)
		switch {
		case !q.Headers.IsZero() && !q.LastByte.IsZero() && now.Sub(q.LastByte) > time.Minute:
			parts = append(parts, "⚠ no data for "+short(now.Sub(q.LastByte)))
		case !q.Headers.IsZero():
			// Streaming normally; the server line carries the speeds.
		case reqErr == nil && sp != nil && sp.InFlight != nil && *sp.InFlight == 0 && age > lostAfter:
			parts = append(parts, "⚠ request not reaching server · "+short(age))
		case reqErr != nil:
			parts = append(parts, "waiting "+short(age))
		}
	}
	if rs.ConsecutiveErr > 0 && now.Sub(rs.LastErrorAt) < 2*time.Minute {
		parts = append(parts, fmt.Sprintf("⚠ connection lost · Claude retries (%d)", rs.ConsecutiveErr))
	}
	if !rs.Qualified && rs.ClaudeVersion != "" {
		parts = append(parts, "untested Claude "+rs.ClaudeVersion)
	}
	if claudePath != "" && rs.ClaudePath != "" && claudePath != rs.ClaudePath {
		parts = append(parts, "Claude changed on disk · restart to use it")
	}
	return strings.Join(parts, " · ")
}
