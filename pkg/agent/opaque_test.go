package agent

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestFrameConnReadDeadlineUnblocksPendingRead(t *testing.T) {
	c := newFrameConn(func([]byte) error { return nil })
	result := make(chan error, 1)
	go func() {
		_, err := c.Read(make([]byte, 1))
		result <- err
	}()
	time.Sleep(20 * time.Millisecond)
	_ = c.SetReadDeadline(time.Now().Add(-time.Second)) // net/http abortPendingRead
	select {
	case err := <-result:
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() || !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("err=%v, want a timeout net.Error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("past read deadline did not unblock Read")
	}
	_ = c.SetReadDeadline(time.Time{})
	c.feed([]byte("hi"))
	buf := make([]byte, 8)
	n, err := c.Read(buf)
	if err != nil || string(buf[:n]) != "hi" {
		t.Fatalf("read after clearing deadline: n=%d err=%v", n, err)
	}
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	if _, err := c.Read(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("future deadline: err=%v", err)
	}
	_ = c.SetWriteDeadline(time.Now().Add(-time.Second))
	if _, err := c.Write([]byte("x")); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("write deadline: err=%v", err)
	}
	_ = c.SetReadDeadline(time.Time{})
	c.closeIncoming()
	if _, err := c.Read(buf); err != io.EOF {
		t.Fatalf("after close: err=%v", err)
	}
}

func TestFrameConnBuffersUntilBoundThenFailsOnlyThatStream(t *testing.T) {
	c := newFrameConn(func([]byte) error { return nil })
	chunk := make([]byte, 32<<10)
	for fed := 0; fed < 2<<20; fed += len(chunk) {
		c.feed(chunk) // must never block or drop below the bound
	}
	var total int
	buf := make([]byte, 64<<10)
	for total < 2<<20 {
		n, err := c.Read(buf)
		if err != nil {
			t.Fatalf("read after %d bytes: %v", total, err)
		}
		total += n
	}
	for fed := 0; fed <= maxStreamBuffer; fed += len(chunk) {
		c.feed(chunk)
	}
	if _, err := c.Read(buf); !errors.Is(err, errStreamBufferExceeded) {
		t.Fatalf("overflow: err=%v", err)
	}
}

func TestReconnectBackoffResetsAfterRegistration(t *testing.T) {
	var b reconnectBackoff
	if got := b.next(false); got != initialBackoff {
		t.Fatalf("first=%v", got)
	}
	if got := b.next(false); got != 2*initialBackoff {
		t.Fatalf("second=%v", got)
	}
	for i := 0; i < 10; i++ {
		b.next(false)
	}
	if got := b.next(false); got != maxBackoff {
		t.Fatalf("capped=%v", got)
	}
	if got := b.next(true); got != initialBackoff {
		t.Fatalf("after a registered session=%v, want %v", got, initialBackoff)
	}
}

func TestProxyNeverForwardsOriginOrReferer(t *testing.T) {
	seen := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		_, _ = io.WriteString(w, "{}")
	}))
	defer upstream.Close()
	h := newLocalInference(Config{UpstreamURL: upstream.URL, CORSOrigins: []string{"https://editor.example"}}, http.DefaultClient)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://editor.example")
	req.Header.Set("Referer", "https://editor.example/chat")
	req.Header.Set("Authorization", "Bearer mup_key_x")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.proxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	got := <-seen
	for _, name := range []string{"Origin", "Referer", "Authorization"} {
		if got.Get(name) != "" {
			t.Fatalf("%s reached the upstream: %q", name, got.Get(name))
		}
	}
	if got.Get("Content-Type") != "application/json" {
		t.Fatal("Content-Type was not forwarded")
	}
}
