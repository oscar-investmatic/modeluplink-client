package tunnel

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	tunnelv1 "github.com/oscar-investmatic/modeluplink-client/proto/tunnel/v1"
	"google.golang.org/protobuf/proto"
)

const (
	CloseEndpointRevoked = 4001 // Terminal: this endpoint was deleted or moved.
	ProtocolVersion      = 1
	ChunkSize            = 32 * 1024
	MaxFrameSize         = 128 * 1024
)

type Writer struct {
	Conn *websocket.Conn
	mu   sync.Mutex
}

func (w *Writer) Write(frame *tunnelv1.Envelope) error {
	frame.ProtocolVersion = ProtocolVersion
	payload, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return w.Conn.WriteMessage(websocket.BinaryMessage, payload)
}

func Read(conn *websocket.Conn) (*tunnelv1.Envelope, error) {
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if messageType != websocket.BinaryMessage {
		return nil, errors.New("tunnel accepts binary protobuf frames only")
	}
	var frame tunnelv1.Envelope
	if err := proto.Unmarshal(payload, &frame); err != nil {
		return nil, err
	}
	if frame.ProtocolVersion != ProtocolVersion {
		return nil, errors.New("unsupported tunnel protocol version")
	}
	return &frame, nil
}

var hopByHop = map[string]struct{}{
	"connection": {}, "proxy-connection": {}, "keep-alive": {}, "proxy-authenticate": {},
	"proxy-authorization": {}, "te": {}, "trailer": {}, "transfer-encoding": {}, "upgrade": {},
}

// privateRequestHeaders never reach the upstream. The agent is the CORS
// enforcement point, so Origin and Referer are consumed there; forwarding them
// would make the upstream's own CORS middleware (Ollama returns 403) reject
// browser requests the agent has already authorised.
var privateRequestHeaders = map[string]struct{}{
	"authorization": {}, "cookie": {}, "cf-connecting-ip": {}, "cf-ipcountry": {},
	"cf-ray": {}, "cf-visitor": {}, "x-forwarded-for": {}, "x-forwarded-host": {},
	"x-forwarded-proto": {}, "x-real-ip": {}, "origin": {}, "referer": {},
}

func RequestHeaders(in http.Header) []*tunnelv1.Header {
	return filteredHeaders(in, true)
}

func ResponseHeaders(in http.Header) []*tunnelv1.Header {
	return filteredHeaders(in, false)
}

func filteredHeaders(in http.Header, request bool) []*tunnelv1.Header {
	out := make([]*tunnelv1.Header, 0, len(in))
	for name, values := range in {
		lower := strings.ToLower(name)
		if _, blocked := hopByHop[lower]; blocked {
			continue
		}
		if request {
			if _, blocked := privateRequestHeaders[lower]; blocked {
				continue
			}
		}
		copied := append([]string(nil), values...)
		out = append(out, &tunnelv1.Header{Name: http.CanonicalHeaderKey(name), Values: copied})
	}
	return out
}

func ApplyHeaders(dst http.Header, headers []*tunnelv1.Header) {
	for _, header := range headers {
		lower := strings.ToLower(header.Name)
		if _, blocked := hopByHop[lower]; blocked {
			continue
		}
		for _, value := range header.Values {
			dst.Add(header.Name, value)
		}
	}
}
