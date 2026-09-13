package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	mtunnel "github.com/oscar-investmatic/modeluplink-client/pkg/tunnel"
	tunnelv1 "github.com/oscar-investmatic/modeluplink-client/proto/tunnel/v1"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

const opaqueTLSMethod = "MODELUPLINK_OPAQUE_TLS_V1"

type rawState struct{ conn *frameConn }

func (r *Runner) endpointTLSConfig() (*tls.Config, error) {
	if r.TLSConfig != nil {
		return r.TLSConfig.Clone(), nil
	}
	host := strings.ToLower(strings.TrimSpace(r.Config.EndpointHost))
	if host == "" || r.Config.CertificateCache == "" {
		return nil, errors.New("endpoint hostname and certificate cache are required for end-to-end TLS")
	}
	if err := os.MkdirAll(r.Config.CertificateCache, 0700); err != nil {
		return nil, err
	}
	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(host),
		Cache:      certificateCache(r.Config.CertificateCache),
	}
	config := manager.TLSConfig()
	// The local HTTP handler currently supports HTTP/1.1. ACME TLS-ALPN-01 is
	// retained so the agent can obtain and renew its own endpoint certificate.
	config.NextProtos = []string{acme.ALPNProto, "http/1.1"}
	config.MinVersion = tls.VersionTLS12
	return config, nil
}

func (s *session) startRaw(parent context.Context, frame *tunnelv1.Envelope) {
	if frame.RequestHead == nil || !strings.EqualFold(frame.RequestHead.Path, s.config.EndpointHost) {
		_ = s.writer.Write(&tunnelv1.Envelope{StreamId: frame.StreamId, Type: tunnelv1.FrameType_FRAME_TYPE_ERROR, Error: "invalid opaque TLS stream"})
		return
	}
	s.mu.Lock()
	if _, exists := s.raw[frame.StreamId]; exists {
		s.mu.Unlock()
		return
	}
	conn := newFrameConn(func(data []byte) error {
		for len(data) > 0 {
			n := len(data)
			if n > mtunnel.ChunkSize {
				n = mtunnel.ChunkSize
			}
			if err := s.writer.Write(&tunnelv1.Envelope{StreamId: frame.StreamId, Type: tunnelv1.FrameType_FRAME_TYPE_DATA, Data: append([]byte(nil), data[:n]...)}); err != nil {
				return err
			}
			data = data[n:]
		}
		return nil
	})
	s.raw[frame.StreamId] = &rawState{conn: conn}
	s.mu.Unlock()
	go s.serveRaw(parent, frame.StreamId, conn)
}

func (s *session) serveRaw(parent context.Context, id string, conn *frameConn) {
	defer func() {
		// The relay may still echo END/DATA for this id after we have removed
		// it (the client socket closing races our END); runOnce ignores frames
		// for unknown streams, so this ordering is safe.
		conn.closeIncoming()
		s.mu.Lock()
		delete(s.raw, id)
		s.mu.Unlock()
		_ = s.writer.Write(&tunnelv1.Envelope{StreamId: id, Type: tunnelv1.FrameType_FRAME_TYPE_END})
	}()
	tlsConn := tls.Server(conn, s.tlsConfig.Clone())
	handshakeCtx, cancel := context.WithTimeout(parent, 30*time.Second)
	err := tlsConn.HandshakeContext(handshakeCtx)
	cancel()
	if err != nil {
		s.logger.Debug("client TLS handshake rejected", "stream_id", id, "error", err)
		return
	}
	listener := &singleConnListener{conn: tlsConn, done: conn.done}
	server := &http.Server{
		BaseContext:       func(net.Listener) context.Context { return parent },
		Handler:           s.inference,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       5 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	_ = server.Serve(listener)
}

func (s *session) writeRaw(frame *tunnelv1.Envelope) bool {
	s.mu.Lock()
	state := s.raw[frame.StreamId]
	s.mu.Unlock()
	if state == nil {
		return false
	}
	state.conn.feed(frame.Data)
	return true
}

func (s *session) endRaw(id string) bool {
	s.mu.Lock()
	state := s.raw[id]
	s.mu.Unlock()
	if state == nil {
		return false
	}
	state.conn.closeIncoming()
	return true
}

// maxStreamBuffer bounds the request bytes buffered per client stream while
// the HTTP handler is not yet reading (for example during the control-plane
// authorize round trip). The tunnel carries no per-stream flow control, so
// the relay pushes at wire speed; blocking the shared WebSocket reader would
// stall every other multiplexed stream, and dropping frames corrupts the TLS
// session. Only the offending stream is failed when the bound is exceeded.
const maxStreamBuffer = 8 << 20

var errStreamBufferExceeded = errors.New("modeluplink: client sent more than 8 MiB before the request was accepted")

// frameConn adapts an ordered sequence of tunnel DATA frames to net.Conn so
// the standard TLS and HTTP servers can run on top of it. Deadlines are real:
// net/http relies on SetReadDeadline in the past to interrupt its background
// read (abortPendingRead) and on ReadHeaderTimeout/IdleTimeout to reap idle
// sessions.
type frameConn struct {
	send     func([]byte) error
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	chunks   [][]byte
	buffered int
	failure  error
	notify   chan struct{}
	current  []byte
	readDl   connDeadline
	writeDl  connDeadline
}

func newFrameConn(send func([]byte) error) *frameConn {
	return &frameConn{send: send, done: make(chan struct{}), notify: make(chan struct{}, 1), readDl: newConnDeadline(), writeDl: newConnDeadline()}
}

func (c *frameConn) feed(data []byte) {
	c.mu.Lock()
	if c.failure != nil || isClosed(c.done) {
		c.mu.Unlock()
		return
	}
	if c.buffered+len(data) > maxStreamBuffer {
		c.failure = errStreamBufferExceeded
		c.chunks, c.buffered = nil, 0
		c.mu.Unlock()
		c.closeIncoming()
		return
	}
	c.chunks = append(c.chunks, append([]byte(nil), data...))
	c.buffered += len(data)
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *frameConn) closeIncoming() { c.once.Do(func() { close(c.done) }) }

func (c *frameConn) Read(p []byte) (int, error) {
	for len(c.current) == 0 {
		c.mu.Lock()
		if c.failure != nil {
			c.mu.Unlock()
			return 0, c.failure
		}
		if len(c.chunks) > 0 {
			// Drain buffered frames before observing closure: END follows DATA
			// on the wire, so buffered bytes are always valid.
			c.current, c.chunks[0] = c.chunks[0], nil
			c.chunks = c.chunks[1:]
			c.buffered -= len(c.current)
			c.mu.Unlock()
			break
		}
		c.mu.Unlock()
		if isClosed(c.done) {
			// Nothing buffered and the stream is closed: EOF, checked before the
			// select so closure wins deterministically over an expired deadline.
			c.mu.Lock()
			pending := len(c.chunks) > 0 || c.failure != nil
			c.mu.Unlock()
			if pending {
				continue
			}
			return 0, io.EOF
		}
		select {
		case <-c.notify:
		case <-c.done:
		case <-c.readDl.wait():
			return 0, os.ErrDeadlineExceeded
		}
	}
	n := copy(p, c.current)
	c.current = c.current[n:]
	return n, nil
}

func (c *frameConn) Write(p []byte) (int, error) {
	select {
	case <-c.done:
		return 0, net.ErrClosed
	case <-c.writeDl.wait():
		return 0, os.ErrDeadlineExceeded
	default:
	}
	if err := c.send(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *frameConn) Close() error                       { c.closeIncoming(); return nil }
func (c *frameConn) LocalAddr() net.Addr                { return tunnelAddr("agent") }
func (c *frameConn) RemoteAddr() net.Addr               { return tunnelAddr("client") }
func (c *frameConn) SetDeadline(t time.Time) error      { c.readDl.set(t); c.writeDl.set(t); return nil }
func (c *frameConn) SetReadDeadline(t time.Time) error  { c.readDl.set(t); return nil }
func (c *frameConn) SetWriteDeadline(t time.Time) error { c.writeDl.set(t); return nil }

// connDeadline mirrors net.Pipe's deadline semantics: wait() returns a channel
// that is closed once the deadline has passed; a zero time clears it.
type connDeadline struct {
	mu     sync.Mutex
	timer  *time.Timer
	cancel chan struct{}
}

func newConnDeadline() connDeadline { return connDeadline{cancel: make(chan struct{})} }

func (d *connDeadline) set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil && !d.timer.Stop() {
		<-d.cancel // the callback is closing the current channel; wait for it
	}
	d.timer = nil
	closed := isClosed(d.cancel)
	if t.IsZero() {
		if closed {
			d.cancel = make(chan struct{})
		}
		return
	}
	if dur := time.Until(t); dur > 0 {
		if closed {
			d.cancel = make(chan struct{})
		}
		cancel := d.cancel
		d.timer = time.AfterFunc(dur, func() { close(cancel) })
		return
	}
	if !closed {
		close(d.cancel)
	}
}

func (d *connDeadline) wait() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cancel
}

func isClosed(c chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

type tunnelAddr string

func (a tunnelAddr) Network() string { return "modeluplink" }
func (a tunnelAddr) String() string  { return string(a) }

type singleConnListener struct {
	conn net.Conn
	once sync.Once
	done <-chan struct{}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = l.conn })
	if conn == nil {
		<-l.done
		return nil, net.ErrClosed
	}
	return conn, nil
}
func (l *singleConnListener) Close() error   { return nil }
func (l *singleConnListener) Addr() net.Addr { return tunnelAddr("local-tls") }
