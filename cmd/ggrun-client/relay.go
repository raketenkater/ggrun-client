package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A slow local model may legitimately send no bytes for many minutes while it
// prefills, so a byte or header deadline cannot tell slow from dead. Liveness
// probes can: HTTP/2 PINGs and TCP keepalive are answered by the peer's network
// stack while the model works, and stop when the path is gone (sleep, network
// change, proxy restart). These bound detection to about a minute.
var (
	h2PingAfter   = 30 * time.Second
	h2PingTimeout = 15 * time.Second
	tcpKeepAlive  = net.KeepAliveConfig{Enable: true, Idle: 15 * time.Second, Interval: 10 * time.Second, Count: 4}
	transportTLS  *tls.Config // tests only; nil uses system roots
)

func remoteTransport() *http.Transport {
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAliveConfig: tcpKeepAlive}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
		HTTP2:                 &http.HTTP2Config{SendPingTimeout: h2PingAfter, PingTimeout: h2PingTimeout},
		TLSClientConfig:       transportTLS,
	}
}

// relayRequest is one Claude request seen by the local relay.
type relayRequest struct {
	ID        int       `json:"id"`
	Path      string    `json:"path"`
	Started   time.Time `json:"started"`
	Headers   time.Time `json:"headers,omitzero"`
	LastByte  time.Time `json:"last_byte,omitzero"`
	Bytes     int64     `json:"bytes"`
	StatusTag int       `json:"status,omitempty"`
}

// relayState is written for the status line. It holds no credentials.
type relayState struct {
	Updated        time.Time      `json:"updated"`
	PID            int            `json:"pid"`
	ClaudeVersion  string         `json:"claude_version,omitempty"`
	ClaudePath     string         `json:"claude_path,omitempty"`
	Qualified      bool           `json:"qualified"`
	InFlight       []relayRequest `json:"in_flight,omitempty"`
	Completed      int            `json:"completed"`
	LastCompleted  time.Time      `json:"last_completed,omitzero"`
	LastError      string         `json:"last_error,omitempty"`
	LastErrorAt    time.Time      `json:"last_error_at,omitzero"`
	ConsecutiveErr int            `json:"consecutive_errors,omitempty"`
}

type relay struct {
	mu       sync.Mutex
	state    relayState
	next     int
	active   map[int]*relayRequest
	path     string
	dirty    chan struct{}
	server   *http.Server
	listener net.Listener
	done     chan struct{}
}

func relayStatePath(name string) (string, error) {
	root, err := configRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "relay", fmt.Sprintf("%s-%d.json", name, os.Getpid())), nil
}

// startRelay serves Claude Code on loopback and forwards to the gateway.
// Authorization comes from Claude's own request; the relay adds nothing.
func startRelay(target string, statePath string, info relayState) (*relay, error) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid gateway URL")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start local relay: %w", err)
	}
	if statePath != "" {
		// State files of crashed sessions are harmless but should not pile up.
		if old, _ := filepath.Glob(filepath.Join(filepath.Dir(statePath), "*.json")); len(old) > 0 {
			for _, f := range old {
				if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) > 24*time.Hour {
					_ = os.Remove(f)
				}
			}
		}
	}
	r := &relay{active: map[int]*relayRequest{}, path: statePath, dirty: make(chan struct{}, 1), listener: ln, done: make(chan struct{})}
	r.state = info
	r.state.PID = os.Getpid()
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			pr.Out.Host = u.Host
		},
		Transport:     &trackingTransport{base: remoteTransport(), r: r},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			if errors.Is(err, context.Canceled) && req.Context().Err() != nil {
				return // Claude cancelled; nothing to report.
			}
			// The tracking transport has already recorded the failure.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": "ggrun-client lost the connection to the server; retrying is safe"}})
		},
	}
	r.server = &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = r.server.Serve(ln) }()
	go r.writer()
	r.mark()
	return r, nil
}

func (r *relay) URL() string { return "http://" + r.listener.Addr().String() }

func (r *relay) Close() {
	_ = r.server.Close()
	close(r.done)
	if r.path != "" {
		_ = os.Remove(r.path)
	}
}

func (r *relay) mark() {
	select {
	case r.dirty <- struct{}{}:
	default:
	}
}

// writer persists at most twice a second, plus a heartbeat so readers can
// tell a live relay from a stale file left by a killed process.
func (r *relay) writer() {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-r.dirty:
		case <-tick.C:
		}
		r.persist()
		select {
		case <-r.done:
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (r *relay) snapshot() relayState {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.state
	s.Updated = time.Now()
	s.InFlight = make([]relayRequest, 0, len(r.active))
	for _, q := range r.active {
		s.InFlight = append(s.InFlight, *q)
	}
	sort.Slice(s.InFlight, func(i, j int) bool { return s.InFlight[i].ID < s.InFlight[j].ID })
	return s
}

func (r *relay) persist() {
	if r.path == "" {
		return
	}
	b, err := json.Marshal(r.snapshot())
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(r.path), 0o700) != nil {
		return
	}
	tmp := r.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil && os.Rename(tmp, r.path) != nil {
		_ = os.Remove(tmp)
	}
}

func (r *relay) begin(path string) *relayRequest {
	r.mu.Lock()
	r.next++
	q := &relayRequest{ID: r.next, Path: path, Started: time.Now()}
	r.active[q.ID] = q
	r.mu.Unlock()
	r.mark()
	return q
}

func (r *relay) update(q *relayRequest, f func(*relayRequest)) {
	r.mu.Lock()
	f(q)
	r.mu.Unlock()
	r.mark()
}

func (r *relay) end(q *relayRequest, err error) {
	r.mu.Lock()
	if _, ok := r.active[q.ID]; !ok {
		r.mu.Unlock()
		return
	}
	delete(r.active, q.ID)
	if err == nil {
		r.state.Completed++
		r.state.LastCompleted = time.Now()
		r.state.ConsecutiveErr = 0
	}
	r.mu.Unlock()
	if err != nil {
		r.fail(err)
	}
	r.mark()
}

func (r *relay) fail(err error) {
	r.mu.Lock()
	r.state.LastError = err.Error()
	r.state.LastErrorAt = time.Now()
	r.state.ConsecutiveErr++
	r.mu.Unlock()
	r.mark()
}

type trackingTransport struct {
	base http.RoundTripper
	r    *relay
}

func (t *trackingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	q := t.r.begin(req.URL.Path)
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		if req.Context().Err() != nil {
			t.r.end(q, nil) // Claude cancelled its own request.
		} else {
			t.r.end(q, err)
		}
		return nil, err
	}
	t.r.update(q, func(q *relayRequest) { q.Headers = time.Now(); q.StatusTag = resp.StatusCode })
	resp.Body = &trackedBody{ReadCloser: resp.Body, q: q, r: t.r, ctx: req.Context()}
	return resp, nil
}

type trackedBody struct {
	io.ReadCloser
	q    *relayRequest
	r    *relay
	ctx  context.Context
	once sync.Once
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.r.update(b.q, func(q *relayRequest) { q.Bytes += int64(n); q.LastByte = time.Now() })
	}
	if err != nil {
		b.finish(err)
	}
	return n, err
}

func (b *trackedBody) Close() error {
	b.finish(nil)
	return b.ReadCloser.Close()
}

func (b *trackedBody) finish(err error) {
	b.once.Do(func() {
		if errors.Is(err, io.EOF) || b.ctx.Err() != nil {
			err = nil
		}
		b.r.end(b.q, err)
	})
}

func readRelayState(path string) (relayState, error) {
	var s relayState
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if time.Since(s.Updated) > 10*time.Second {
		return s, fmt.Errorf("relay state is stale")
	}
	return s, nil
}

// withLoopbackNoProxy keeps Claude's requests to the relay off any
// user-configured HTTP proxy, which could not reach this loopback address.
func withLoopbackNoProxy(env []string) []string {
	out := make([]string, 0, len(env)+2)
	seen := map[string]bool{}
	for _, pair := range env {
		k, v, _ := strings.Cut(pair, "=")
		if k == "NO_PROXY" || k == "no_proxy" {
			seen[k] = true
			if v != "" {
				v += ","
			}
			pair = k + "=" + v + "127.0.0.1,localhost"
		}
		out = append(out, pair)
	}
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		if !seen[k] {
			out = append(out, k+"=127.0.0.1,localhost")
		}
	}
	return out
}
