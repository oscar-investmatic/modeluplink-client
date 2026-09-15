package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
)

// Open WebUI sends a title request with the same key as the chat it belongs
// to. On a trial endpoint the second request must wait, not fail with 429.
func TestSameKeyRequestQueuesBehindItsOwnRequest(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, trialAuthorize, trialUsageLeft)
	started := make(chan struct{}, 2)
	finish := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-finish
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer upstream.Close()
	defer close(finish)
	config := Config{EndpointID: "ep_1", AgentToken: "agent-token", ControlURL: control.server.URL, UpstreamURL: upstream.URL,
		Engine: "openai_compatible", SharedModels: []string{"shared:1"}, scheduler: newModelScheduler("")}
	handler := newLocalInference(config, http.DefaultClient)
	key, _, err := security.NewToken("mup", "key_1")
	if err != nil {
		t.Fatal(err)
	}
	send := func() <-chan *httptest.ResponseRecorder {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"shared:1","messages":[]}`))
			r.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			done <- w
		}()
		return done
	}
	first := send()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first request never reached the model")
	}
	second := send()
	waitForQueue(t, config.scheduler, 1)
	select {
	case w := <-second:
		t.Fatalf("same-key request did not queue: %d %s", w.Code, w.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
	finish <- struct{}{}
	if w := <-first; w.Code != http.StatusOK {
		t.Fatalf("first request: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("queued request never reached the model")
	}
	finish <- struct{}{}
	if w := <-second; w.Code != http.StatusOK {
		t.Fatalf("queued request: %d %s", w.Code, w.Body.String())
	}
}
