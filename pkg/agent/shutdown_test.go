package agent

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	mtunnel "github.com/oscar-investmatic/modeluplink-client/pkg/tunnel"
	tunnelv1 "github.com/oscar-investmatic/modeluplink-client/proto/tunnel/v1"
)

func TestRunnerCancellationClosesLiveRelay(t *testing.T) {
	for _, registered := range []bool{false, true} {
		name := "awaiting_registration"
		if registered {
			name = "registered_idle"
		}
		t.Run(name, func(t *testing.T) {
			ready := make(chan struct{})
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, req, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				defer close(closed)
				if _, err = mtunnel.Read(conn); err != nil {
					t.Error(err)
					return
				}
				if registered {
					writer := &mtunnel.Writer{Conn: conn}
					if err = writer.Write(&tunnelv1.Envelope{Type: tunnelv1.FrameType_FRAME_TYPE_REGISTERED}); err != nil {
						t.Error(err)
						return
					}
				}
				close(ready)
				for {
					if _, err = mtunnel.Read(conn); err != nil {
						return
					}
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner := Runner{Config: Config{RelayURL: "ws" + strings.TrimPrefix(server.URL, "http"), UpstreamURL: "http://127.0.0.1:11434"}, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
			done := make(chan error, 1)
			go func() { done <- runner.Run(ctx) }()
			select {
			case <-ready:
			case <-time.After(3 * time.Second):
				t.Fatal("relay registration did not arrive")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("agent remained connected after cancellation")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("relay socket remained open")
			}
		})
	}
}
