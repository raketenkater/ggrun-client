package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blackhole forwards TCP until cut, then keeps both sockets open while
// dropping every byte: the half-open path a sleeping laptop or a restarted
// proxy leaves behind. Neither side sees a close or a reset.
type blackhole struct {
	ln  net.Listener
	cut atomic.Bool
}

func newBlackhole(t *testing.T, target string) *blackhole {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &blackhole{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			u, err := net.Dial("tcp", target)
			if err != nil {
				c.Close()
				continue
			}
			t.Cleanup(func() { c.Close(); u.Close() })
			pipe := func(dst, src net.Conn) {
				buf := make([]byte, 32<<10)
				for {
					n, err := src.Read(buf)
					if err != nil {
						return
					}
					if !b.cut.Load() {
						if _, err := dst.Write(buf[:n]); err != nil {
							return
						}
					}
				}
			}
			go pipe(u, c)
			go pipe(c, u)
		}
	}()
	return b
}

func (b *blackhole) Addr() string { return b.ln.Addr().String() }

// h2Gateway is a TLS HTTP/2 server like the Tailscale/HTTPS front of a gateway.
func h2Gateway(t *testing.T, h http.Handler) *httptest.Server {
	s := httptest.NewUnstartedServer(h)
	s.EnableHTTP2 = true
	s.StartTLS()
	t.Cleanup(s.Close)
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	oldTLS, oldAfter, oldTimeout := transportTLS, h2PingAfter, h2PingTimeout
	transportTLS = &tls.Config{RootCAs: pool}
	h2PingAfter, h2PingTimeout = 500*time.Millisecond, 500*time.Millisecond
	t.Cleanup(func() { transportTLS, h2PingAfter, h2PingTimeout = oldTLS, oldAfter, oldTimeout })
	return s
}

func TestRelayFailsDeadPathButKeepsSlowModel(t *testing.T) {
	var arrived atomic.Int64
	slow := 3 * time.Second // several ping intervals with no response bytes
	gw := h2Gateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("gateway path is not HTTP/2: %s", r.Proto)
		}
		arrived.Add(1)
		if r.URL.Path == "/v1/slow" {
			time.Sleep(slow)
			io.WriteString(w, `{"ok":true}`)
			return
		}
		<-r.Context().Done() // a prefill that never finishes
	}))
	bh := newBlackhole(t, strings.TrimPrefix(gw.URL, "https://"))
	state := filepath.Join(t.TempDir(), "relay.json")
	rl, err := startRelay("https://"+bh.Addr(), state, relayState{ClaudeVersion: "2.1.316", Qualified: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()

	start := time.Now()
	resp, err := http.Post(rl.URL()+"/v1/slow", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != `{"ok":true}` || time.Since(start) < slow {
		t.Fatalf("slow but live model was not allowed to finish: %d %q after %s", resp.StatusCode, b, time.Since(start))
	}

	done := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Post(rl.URL()+"/v1/messages", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Error(err)
		}
		done <- resp
	}()
	for arrived.Load() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	bh.cut.Store(true)
	cut := time.Now()
	select {
	case resp := <-done:
		if resp == nil {
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("dead path returned %d, want 502 so Claude retries", resp.StatusCode)
		}
		if waited := time.Since(cut); waited > 5*time.Second {
			t.Fatalf("dead path detected after %s", waited)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("relay waited on a dead path")
	}
	rl.persist()
	s, err := readRelayState(state)
	if err != nil {
		t.Fatal(err)
	}
	if s.Completed != 1 || s.ConsecutiveErr == 0 || s.LastError == "" || len(s.InFlight) != 0 {
		t.Fatalf("relay state did not record the outcome: %+v", s)
	}
}

func TestRelayStreamsAndTracksBytes(t *testing.T) {
	gw := h2Gateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer client-secret" {
			t.Error("relay changed client headers")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 3; i++ {
			io.WriteString(w, "event: ping\ndata: {}\n\n")
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	state := filepath.Join(t.TempDir(), "relay.json")
	rl, err := startRelay(gw.URL, state, relayState{})
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()
	req, _ := http.NewRequest("POST", rl.URL()+"/v1/messages", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer client-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := bufio.NewReader(resp.Body).ReadString('\n')
	if first != "event: ping\n" {
		t.Fatalf("stream not flushed: %q", first)
	}
	mid := rl.snapshot()
	if len(mid.InFlight) != 1 || mid.InFlight[0].Headers.IsZero() || mid.InFlight[0].Bytes == 0 {
		t.Fatalf("in-flight request not tracked: %+v", mid)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for len(rl.snapshot().InFlight) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s := rl.snapshot(); len(s.InFlight) != 0 || s.Completed != 1 || s.ConsecutiveErr != 0 {
		t.Fatalf("completed stream not recorded: %+v", s)
	}
}

func TestReviewFailsFastOnDeadPath(t *testing.T) {
	var arrived atomic.Int64
	gw := h2Gateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived.Add(1)
		<-r.Context().Done()
	}))
	bh := newBlackhole(t, strings.TrimPrefix(gw.URL, "https://"))
	c := connection{URL: "https://" + bh.Addr(), Token: strings.Repeat("k", 40)}
	c.Profile.MainAlias = "local"
	var once sync.Once
	go func() {
		for arrived.Load() == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		once.Do(func() { bh.cut.Store(true) })
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	_, err := evaluateReview(ctx, c, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "lost the server connection") {
		t.Fatalf("review on a dead path: %v", err)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || time.Since(start) > 8*time.Second {
		t.Fatalf("review waited %s for a dead path", time.Since(start))
	}
}

func TestLoopbackNoProxyKeepsUserEntries(t *testing.T) {
	got := strings.Join(withLoopbackNoProxy([]string{"NO_PROXY=corp.example", "HTTPS_PROXY=http://proxy:3128"}), "\n")
	for _, want := range []string{"NO_PROXY=corp.example,127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost", "HTTPS_PROXY=http://proxy:3128"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q lacks %q", got, want)
		}
	}
}
