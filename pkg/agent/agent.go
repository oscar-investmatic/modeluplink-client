package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	mtunnel "github.com/oscar-investmatic/modeluplink-client/pkg/tunnel"
	tunnelv1 "github.com/oscar-investmatic/modeluplink-client/proto/tunnel/v1"
)

var ErrEndpointRevoked = errors.New("endpoint moved or removed")

var Version = "0.1.0-dev"

type Config struct {
	EndpointID       string
	EndpointHost     string
	AgentToken       string
	Engine           string
	RelayURL         string
	ControlURL       string
	CertificateCache string
	UpstreamURL      string
	UpstreamKey      string
	AllowLAN         bool
	CORSOrigins      []string
	SharedModels     []string
	ActivityPath     string
	scheduler        *modelScheduler
}

type Runner struct {
	Config    Config
	Logger    *slog.Logger
	TLSConfig *tls.Config // test/development injection; production uses ACME
}

func (r *Runner) Run(ctx context.Context) error {
	if r.Logger == nil {
		r.Logger = slog.Default()
	}
	if err := ValidateUpstream(r.Config.UpstreamURL, r.Config.AllowLAN); err != nil {
		return err
	}
	if r.Config.Engine == "openai_compatible" && len(r.Config.SharedModels) == 0 {
		return errors.New("select at least one model before sharing")
	}
	if r.Config.SharedModels != nil {
		r.Config.scheduler = newModelScheduler(r.Config.ActivityPath)
		go r.Config.scheduler.monitor(ctx)
	}
	var backoff reconnectBackoff
	for {
		registered, err := r.runOnce(ctx)
		if websocket.IsCloseError(err, mtunnel.CloseEndpointRevoked) {
			return ErrEndpointRevoked
		}
		if ctx.Err() != nil {
			return nil
		}
		wait := backoff.next(registered)
		r.Logger.Warn("relay disconnected; reconnecting", "error", err, "backoff", wait)
		jitter := time.Duration(rand.Int64N(int64(wait/2 + 1)))
		timer := time.NewTimer(wait + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

const (
	initialBackoff = time.Second
	maxBackoff     = 30 * time.Second
)

// reconnectBackoff doubles the wait between failed connection attempts and
// resets once a session was registered, so a long-lived tunnel that drops
// once does not inherit the delay accumulated by earlier outages.
type reconnectBackoff struct{ current time.Duration }

func (b *reconnectBackoff) next(registered bool) time.Duration {
	switch {
	case registered || b.current == 0:
		b.current = initialBackoff
	case b.current < maxBackoff:
		b.current = min(b.current*2, maxBackoff)
	}
	return b.current
}

// runOnce reports whether the relay accepted the registration before the
// session ended.
func (r *Runner) runOnce(ctx context.Context) (registered bool, err error) {
	ctx, cancelSession := context.WithCancel(ctx)
	defer cancelSession()
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, r.Config.RelayURL, http.Header{"User-Agent": []string{"modeluplink-agent/" + Version}})
	if err != nil {
		if resp != nil {
			return false, fmt.Errorf("relay returned %s", resp.Status)
		}
		return false, err
	}
	defer conn.Close()
	// DialContext only covers dialing. Interrupt an established socket as well,
	// so a Stop/sign-out cannot remain blocked reading relay heartbeats.
	stopCancellation := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancellation()
	conn.SetReadLimit(mtunnel.MaxFrameSize)
	writer := &mtunnel.Writer{Conn: conn}
	register := &tunnelv1.Envelope{
		Type: tunnelv1.FrameType_FRAME_TYPE_REGISTER,
		Register: &tunnelv1.Register{
			EndpointId:   r.Config.EndpointID,
			AgentToken:   r.Config.AgentToken,
			AgentVersion: Version,
			Engine:       r.Config.Engine,
			Capabilities: []string{"opaque-tls-v1", "multiplex", "streaming", "cancel"},
		},
	}
	if err := writer.Write(register); err != nil {
		return false, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	frame, err := mtunnel.Read(conn)
	if err != nil {
		return false, err
	}
	if frame.Type != tunnelv1.FrameType_FRAME_TYPE_REGISTERED {
		return false, errors.New("relay rejected registration")
	}
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	r.Logger.Info("connected to relay", "endpoint_id", r.Config.EndpointID, "engine", r.Config.Engine)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			MaxIdleConnsPerHost:   32,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 5 * time.Minute,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("upstream redirects are disabled")
		},
	}
	tlsConfig, err := r.endpointTLSConfig()
	if err != nil {
		return true, err
	}
	session := &session{
		config:    r.Config,
		logger:    r.Logger,
		writer:    writer,
		raw:       map[string]*rawState{},
		tlsConfig: tlsConfig,
		inference: newLocalInference(r.Config, client),
	}
	defer func() { cancelSession(); session.cancelAll(); session.inference.drain() }()
	pingDone := make(chan struct{})
	defer close(pingDone)
	go session.heartbeat(pingDone, conn)
	for {
		frame, err = mtunnel.Read(conn)
		if err != nil {
			session.cancelAll()
			return true, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		switch frame.Type {
		case tunnelv1.FrameType_FRAME_TYPE_REQUEST_HEAD:
			if frame.RequestHead == nil || frame.RequestHead.Method != opaqueTLSMethod {
				return true, errors.New("relay attempted a non-encrypted inference stream")
			}
			session.startRaw(ctx, frame)
		case tunnelv1.FrameType_FRAME_TYPE_DATA:
			// Frames for unknown streams are expected: the relay's END/DATA for a
			// client socket races the agent's own END after a rejected handshake
			// or an early response. They must never take down the shared tunnel.
			if !session.writeRaw(frame) {
				r.Logger.Debug("ignored data for an unknown stream", "stream_id", frame.StreamId)
			}
		case tunnelv1.FrameType_FRAME_TYPE_END, tunnelv1.FrameType_FRAME_TYPE_CANCEL:
			if !session.endRaw(frame.StreamId) {
				r.Logger.Debug("ignored close for an unknown stream", "stream_id", frame.StreamId)
			}
		case tunnelv1.FrameType_FRAME_TYPE_PING:
			_ = writer.Write(&tunnelv1.Envelope{Type: tunnelv1.FrameType_FRAME_TYPE_PONG})
		case tunnelv1.FrameType_FRAME_TYPE_PONG:
		default:
			return true, fmt.Errorf("unexpected relay frame %s", frame.Type.String())
		}
	}
}

type session struct {
	config    Config
	logger    *slog.Logger
	writer    *mtunnel.Writer
	mu        sync.Mutex
	raw       map[string]*rawState
	tlsConfig *tls.Config
	inference *localInference
}

func (s *session) cancelAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, state := range s.raw {
		state.conn.closeIncoming()
		delete(s.raw, id)
	}
}

func (s *session) heartbeat(done <-chan struct{}, conn *websocket.Conn) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := s.writer.Write(&tunnelv1.Envelope{Type: tunnelv1.FrameType_FRAME_TYPE_PING}); err != nil {
				_ = conn.Close()
				return
			}
		}
	}
}

func allowed(method, path string) bool {
	switch method + " " + path {
	case "GET /v1/models", "POST /v1/chat/completions", "POST /v1/completions", "POST /v1/responses", "POST /v1/embeddings":
		return true
	default:
		return false
	}
}

func ValidateUpstream(raw string, allowLAN bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid upstream URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("upstream URL must use http or https")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("upstream URL cannot contain credentials, query, or fragment")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("upstream URL requires a host")
	}
	if allowLAN {
		return nil
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("upstream must be loopback; pass --allow-lan-upstream to explicitly allow a LAN target")
	}
	return nil
}
