package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
)

func TestModelSelectionFiltersDiscoveryAndBlocksInference(t *testing.T) {
	control := newFakeControl(t, 200, paidAuthorize, trialUsageLeft)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method == "GET" {
			io.WriteString(w, `{"object":"list","data":[{"id":"allowed:1","object":"model"},{"id":"private:1","object":"model"}]}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"model":"allowed:1"`) {
			t.Errorf("unexpected upstream body %s", raw)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer upstream.Close()
	config := Config{EndpointID: "ep_1", AgentToken: "agent-token", ControlURL: control.server.URL, UpstreamURL: upstream.URL, SharedModels: []string{"allowed:1"}}
	handler := newLocalInference(config, http.DefaultClient)
	key, _, err := security.NewToken("mup", "key_1")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	listing := request("GET", "/v1/models", "")
	if listing.Code != 200 || strings.Contains(listing.Body.String(), "private") || !strings.Contains(listing.Body.String(), "allowed:1") {
		t.Fatal(listing.Body.String())
	}
	for _, path := range []string{"/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/embeddings"} {
		before := calls.Load()
		w := request("POST", path, `{"model":"private:1"}`)
		if w.Code != 403 || calls.Load() != before {
			t.Fatalf("unshared model reached upstream: %s %d", path, w.Code)
		}
	}
	for _, body := range []string{`{"model":"allowed:1","model":"private:1"}`, `{"model":"allowed:1"} {}`, `[]`} {
		if w := request("POST", "/v1/chat/completions", body); w.Code != 400 {
			t.Fatalf("ambiguous request accepted: %s %d", body, w.Code)
		}
	}
	if w := request("POST", "/v1/chat/completions", `{"model":"allowed:1","messages":[]}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestModelSchedulerBoundsAndCancelsQueue(t *testing.T) {
	s := newModelScheduler("")
	release, err := s.acquire(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			release, err := s.acquire(ctx, "second")
			if release != nil {
				release()
			}
			results <- err
		}()
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		waiting := s.activity.Waiting
		s.mu.Unlock()
		if waiting == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queue never filled")
		}
		time.Sleep(time.Millisecond)
	}
	if next, err := s.acquire(context.Background(), "overflow"); err == nil {
		next()
		t.Fatal("unbounded queue")
	}
	cancel()
	for i := 0; i < 4; i++ {
		select {
		case err := <-results:
			if err == nil {
				t.Fatal("cancelled waiter admitted")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("waiter stuck")
		}
	}
	release()
	s.mu.Lock()
	activity := s.activity
	s.mu.Unlock()
	if activity.Running != 0 || activity.Waiting != 0 {
		t.Fatalf("leaked slots: %+v", activity)
	}
	next, err := s.acquire(context.Background(), "third")
	if err != nil {
		t.Fatal(err)
	}
	next()
}

func waitForQueue(t *testing.T, s *modelScheduler, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		waiting := s.activity.Waiting
		s.mu.Unlock()
		if waiting == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue length %d, want %d", waiting, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestOneFriendQueuesButCannotFillTheQueue(t *testing.T) {
	s := newModelScheduler("")
	release, err := s.acquireForKey(context.Background(), "large-model", "friend-one")
	if err != nil {
		t.Fatal(err)
	}
	// The same credential queues behind its own running request instead of
	// failing: apps send background requests (titles, tags) with one key.
	results := make(chan error, 3)
	acquire := func(key string) {
		next, err := s.acquireForKey(context.Background(), "large-model", key)
		if next != nil {
			next()
		}
		results <- err
	}
	for i := 1; i < perKeyOutstanding; i++ {
		go acquire("friend-one")
	}
	waitForQueue(t, s, perKeyOutstanding-1)
	if _, err = s.acquireForKey(context.Background(), "large-model", "friend-one"); err == nil {
		t.Fatal("one friend exceeded its outstanding request cap")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.acquireForKey(ctx, "large-model", "friend-two"); err == nil {
		t.Fatal("cancelled friend request admitted")
	}
	// Another friend still finds a place in the queue.
	go acquire("friend-two")
	waitForQueue(t, s, perKeyOutstanding)
	release()
	for i := 0; i < perKeyOutstanding; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal("queued request failed:", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("queued request stuck")
		}
	}
	s.mu.Lock()
	keys, activity := len(s.keys), s.activity
	s.mu.Unlock()
	if keys != 0 || activity.Running != 0 || activity.Waiting != 0 {
		t.Fatalf("leaked credential or slot state: keys=%d %+v", keys, activity)
	}
}
