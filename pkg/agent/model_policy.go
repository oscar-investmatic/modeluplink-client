package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// sharedModel checks the exact model ID selected for this endpoint.
// checkModel bypasses selection only for older, non-OpenAI-compatible profiles
// whose SharedModels field is absent. An explicit empty list shares no models.
func (h *localInference) sharedModel(name string) bool {
	for _, allowed := range h.config.SharedModels {
		if name == allowed {
			return true
		}
	}
	return false
}
func (h *localInference) checkModel(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Method != http.MethodPost || h.config.SharedModels == nil && h.config.Engine != "openai_compatible" {
		return "", true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInferenceBody))
	if err != nil {
		h.writeError(w, 413, "request_too_large", "request exceeds 32 MiB")
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		h.writeError(w, 400, "invalid_request", "expected a JSON object")
		return "", false
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			break
		}
		key, ok := token.(string)
		if !ok {
			err = errors.New("invalid key")
			break
		}
		if _, duplicate := fields[key]; duplicate {
			err = errors.New("duplicate key")
			break
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			break
		}
		fields[key] = value
	}
	if err == nil {
		_, err = decoder.Token()
	}
	var trailing any
	if err != nil || decoder.Decode(&trailing) != io.EOF {
		h.writeError(w, 400, "invalid_request", "invalid or ambiguous JSON request")
		return "", false
	}
	var model string
	if json.Unmarshal(fields["model"], &model) != nil || !h.sharedModel(model) {
		h.writeError(w, 403, "model_not_shared", "this model is not shared by this endpoint; choose a model from /v1/models")
		return "", false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return model, true
}
func (h *localInference) modelList(w http.ResponseWriter, response *http.Response) {
	var body struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxInferenceBody+1))
	if err != nil || len(raw) > maxInferenceBody || json.Unmarshal(raw, &body) != nil {
		h.writeError(w, 502, "upstream_error", "could not read the local model list")
		return
	}
	filtered := make([]json.RawMessage, 0)
	for _, entry := range body.Data {
		var model struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(entry, &model) == nil && h.sharedModel(model.ID) {
			filtered = append(filtered, entry)
		}
	}
	body.Data = filtered
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	_ = json.NewEncoder(w).Encode(body)
}

// Activity reports model occupancy and queue lengths without prompt content.
type Activity struct {
	Model     string    `json:"model"`
	Running   int       `json:"running"`
	Waiting   int       `json:"waiting"`
	UpdatedAt time.Time `json:"updated_at"`
}

const (
	// maxWaiting bounds the queue behind the single inference slot.
	maxWaiting = 4
	// queueWait lets a request wait behind a slow generation, such as a
	// thinking model writing a chat title. It stays under the 5-minute idle
	// timeouts of the relay and the agent's TLS server.
	queueWait = 2 * time.Minute
	// perKeyOutstanding lets one credential hold the running slot and two
	// queue places, so apps that send background requests with the same key
	// queue instead of failing, while other friends always keep a place.
	perKeyOutstanding = 3
)

// One inference at a time, with up to maxWaiting requests waiting for at most
// queueWait. Discovery does not take an inference slot. Cancelled clients
// leave the queue.
type modelScheduler struct {
	keys     map[string]int
	mu       sync.Mutex
	slot     chan struct{}
	activity Activity
	path     string
}

func newModelScheduler(path string) *modelScheduler {
	return &modelScheduler{slot: make(chan struct{}, 1), path: path}
}
func (s *modelScheduler) publishLocked() {
	s.activity.UpdatedAt = time.Now().UTC()
	if s.path == "" {
		return
	}
	data, _ := json.Marshal(s.activity)
	if os.WriteFile(s.path+".tmp", data, 0600) == nil {
		_ = os.Rename(s.path+".tmp", s.path)
	}
}
func (s *modelScheduler) monitor(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	defer os.Remove(s.path)
	for {
		s.mu.Lock()
		s.publishLocked()
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *modelScheduler) acquire(ctx context.Context, model string) (func(), error) {
	s.mu.Lock()
	select {
	case s.slot <- struct{}{}:
		s.activity.Model = model
		s.activity.Running = 1
		s.publishLocked()
		s.mu.Unlock()
		return s.release, nil
	default:
	}
	if s.activity.Waiting >= maxWaiting {
		s.mu.Unlock()
		return nil, errors.New("queue full")
	}
	s.activity.Waiting++
	s.publishLocked()
	s.mu.Unlock()
	wait, cancel := context.WithTimeout(ctx, queueWait)
	defer cancel()
	select {
	case s.slot <- struct{}{}:
		s.mu.Lock()
		s.activity.Waiting--
		s.activity.Model = model
		s.activity.Running = 1
		s.publishLocked()
		s.mu.Unlock()
		if ctx.Err() != nil {
			s.release()
			return nil, ctx.Err()
		}
		return s.release, nil
	case <-wait.Done():
		s.mu.Lock()
		s.activity.Waiting--
		s.publishLocked()
		s.mu.Unlock()
		return nil, wait.Err()
	}
}
func (s *modelScheduler) release() {
	s.mu.Lock()
	s.activity.Running = 0
	s.activity.Model = ""
	s.publishLocked()
	<-s.slot
	s.mu.Unlock()
}

// A per-credential cap on outstanding requests prevents one friend from
// occupying every queue slot. The runtime still owns GPU scheduling.
func (s *modelScheduler) acquireForKey(ctx context.Context, model, key string) (func(), error) {
	s.mu.Lock()
	if s.keys == nil {
		s.keys = map[string]int{}
	}
	if s.keys[key] >= perKeyOutstanding {
		s.mu.Unlock()
		return nil, errors.New("this key already has too many outstanding requests")
	}
	s.keys[key]++
	s.mu.Unlock()
	forget := func() {
		s.mu.Lock()
		if s.keys[key]--; s.keys[key] <= 0 {
			delete(s.keys, key)
		}
		s.mu.Unlock()
	}
	release, err := s.acquire(ctx, model)
	if err != nil {
		forget()
		return nil, err
	}
	return func() { release(); forget() }, nil
}
