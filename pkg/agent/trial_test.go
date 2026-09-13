package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
)

// fakeControl mimics the control plane's agent routes per the free tier contract.
type fakeControl struct {
	t              *testing.T
	mu             sync.Mutex
	authorizeBody  string
	authorizeCode  int
	usageBody      string
	usageCalls     atomic.Int32
	usageCompleted atomic.Int32
	usageTransfer  atomic.Int64
	server         *httptest.Server
}

func newFakeControl(t *testing.T, authorizeCode int, authorizeBody, usageBody string) *fakeControl {
	f := &fakeControl{t: t, authorizeCode: authorizeCode, authorizeBody: authorizeBody, usageBody: usageBody}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer agent-token" {
			t.Errorf("agent authorization = %q", r.Header.Get("Authorization"))
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/agent/authorize":
			w.WriteHeader(f.authorizeCode)
			_, _ = io.WriteString(w, f.authorizeBody)
		case "/v1/agent/usage":
			var input struct {
				EndpointID    string `json:"endpoint_id"`
				Completed     int    `json:"completed"`
				TransferBytes int64  `json:"transfer_bytes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.EndpointID != "ep_1" || input.Completed < 0 {
				t.Errorf("usage body invalid: %+v %v", input, err)
			}
			f.usageCalls.Add(1)
			f.usageCompleted.Add(int32(input.Completed))
			f.usageTransfer.Add(input.TransferBytes)
			_, _ = io.WriteString(w, f.usageBody)
		default:
			t.Errorf("unexpected control path %s", r.URL.Path)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeControl) set(code int, authorizeBody, usageBody string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authorizeCode, f.authorizeBody, f.usageBody = code, authorizeBody, usageBody
}

const (
	trialAuthorize  = `{"key_id":"key_1","trial":true,"max_concurrency":1,"trial_remaining":5,"trial_transfer_remaining":250000000,"trial_expires_at":"2099-01-01T00:00:00Z"}`
	paidAuthorize   = `{"key_id":"key_1","trial":false,"max_concurrency":4}`
	trialUsageLeft  = `{"trial":true,"trial_remaining":4,"trial_transfer_remaining":249999000,"trial_status":"active"}`
	trialUsageEmpty = `{"trial":true,"trial_remaining":0,"trial_transfer_remaining":249999000,"trial_status":"exhausted"}`
)

type trialFixture struct {
	control   *fakeControl
	upstream  *httptest.Server
	handler   *localInference
	public    *httptest.Server
	key       string
	upstreamN atomic.Int32
}

// newTrialFixture wires a fake control plane, a fake upstream, and the agent's
// inference handler behind a real HTTP server so client cancellation propagates.
func newTrialFixture(t *testing.T, control *fakeControl, upstream http.HandlerFunc) *trialFixture {
	f := &trialFixture{control: control}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.upstreamN.Add(1)
		upstream(w, r)
	}))
	t.Cleanup(f.upstream.Close)
	f.handler = newLocalInference(Config{EndpointID: "ep_1", AgentToken: "agent-token", ControlURL: control.server.URL, UpstreamURL: f.upstream.URL}, http.DefaultClient)
	f.public = httptest.NewServer(f.handler)
	t.Cleanup(f.public.Close)
	key, _, err := security.NewToken("mup", "key_1")
	if err != nil {
		t.Fatal(err)
	}
	f.key = key
	return f
}

func (f *trialFixture) do(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, f.public.URL+path, strings.NewReader(`{"model":"m","messages":[]}`))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+f.key)
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

func (f *trialFixture) status(t *testing.T, method, path string) (int, string) {
	t.Helper()
	resp, err := f.do(context.Background(), method, path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// waitUsageSettled blocks until no request is in flight and no usage report
// is queued or outstanding.
func (f *trialFixture) waitUsageSettled(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.handler.mu.Lock()
		settled := f.handler.inFlight == 0 && f.handler.pendingUsage == 0 && !f.handler.usageRunning
		f.handler.mu.Unlock()
		if settled {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("usage reporting did not settle")
}

func okUpstream(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"id":"chatcmpl-1","choices":[]}`)
}

func TestTrialAuthorizeEnforcesConcurrencyOfOne(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, trialAuthorize, trialUsageLeft)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	f := newTrialFixture(t, control, func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		okUpstream(w, nil)
	})
	first := make(chan int, 1)
	go func() {
		code, _ := f.status(t, http.MethodPost, "/v1/chat/completions")
		first <- code
	}()
	<-started
	code, body := f.status(t, http.MethodPost, "/v1/chat/completions")
	if code != http.StatusTooManyRequests || !strings.Contains(body, "concurrency_limit_exceeded") {
		t.Fatalf("second concurrent trial request = %d %s", code, body)
	}
	close(release)
	if code = <-first; code != http.StatusOK {
		t.Fatalf("first request = %d", code)
	}
	f.waitUsageSettled(t)
	if got := control.usageCalls.Load(); got != 1 {
		t.Fatalf("usage calls = %d", got)
	}
}

func TestPaidAuthorizeAllowsFourConcurrentAndReportsActivity(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, paidAuthorize, `{"trial":false}`)
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(4)
	f := newTrialFixture(t, control, func(w http.ResponseWriter, _ *http.Request) {
		started.Done()
		<-release
		okUpstream(w, nil)
	})
	codes := make(chan int, 4)
	for i := 0; i < 4; i++ {
		go func() {
			code, _ := f.status(t, http.MethodPost, "/v1/chat/completions")
			codes <- code
		}()
	}
	started.Wait()
	if code, _ := f.status(t, http.MethodPost, "/v1/completions"); code != http.StatusTooManyRequests {
		t.Fatalf("fifth paid request = %d", code)
	}
	close(release)
	for i := 0; i < 4; i++ {
		if code := <-codes; code != http.StatusOK {
			t.Fatalf("paid request = %d", code)
		}
	}
	f.waitUsageSettled(t)
	if calls, completed := control.usageCalls.Load(), control.usageCompleted.Load(); calls < 1 || completed != 4 {
		t.Fatalf("paid activity calls=%d completed=%d", calls, completed)
	}
}

func TestTrialModelsRouteStartsClockWithoutConsumingAllowance(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, trialAuthorize, trialUsageLeft)
	f := newTrialFixture(t, control, okUpstream)
	if code, _ := f.status(t, http.MethodGet, "/v1/models"); code != http.StatusOK {
		t.Fatalf("models = %d", code)
	}
	f.waitUsageSettled(t)
	if calls, completed, transfer := control.usageCalls.Load(), control.usageCompleted.Load(), control.usageTransfer.Load(); calls != 1 || completed != 0 || transfer != 0 {
		t.Fatalf("model discovery usage calls=%d completed=%d transfer=%d", calls, completed, transfer)
	}
}

func TestTrialCompletedChatReportsExactlyOneUsage(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, trialAuthorize, trialUsageLeft)
	f := newTrialFixture(t, control, okUpstream)
	if code, _ := f.status(t, http.MethodPost, "/v1/chat/completions"); code != http.StatusOK {
		t.Fatalf("chat = %d", code)
	}
	f.waitUsageSettled(t)
	if calls, completed := control.usageCalls.Load(), control.usageCompleted.Load(); calls != 1 || completed != 1 {
		t.Fatalf("usage calls = %d completed = %d", calls, completed)
	}
	wantTransfer := int64(len(`{"model":"m","messages":[]}`) + len(`{"id":"chatcmpl-1","choices":[]}`))
	if got := control.usageTransfer.Load(); got != wantTransfer {
		t.Fatalf("usage transfer = %d, want %d", got, wantTransfer)
	}
	f.handler.mu.Lock()
	remaining := f.handler.trialRemaining
	f.handler.mu.Unlock()
	if remaining != 4 {
		t.Fatalf("trialRemaining after usage = %d", remaining)
	}
}

func TestTrialUpstreamErrorReportsNoUsage(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, trialAuthorize, trialUsageLeft)
	f := newTrialFixture(t, control, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model crashed", http.StatusInternalServerError)
	})
	if code, _ := f.status(t, http.MethodPost, "/v1/chat/completions"); code != http.StatusInternalServerError {
		t.Fatalf("chat = %d", code)
	}
	f.waitUsageSettled(t)
	if got := control.usageCalls.Load(); got != 0 {
		t.Fatalf("usage calls after upstream error = %d", got)
	}
}

func streamingUpstream(firstChunk chan<- struct{}, release <-chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case firstChunk <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func TestTrialStreamCancelledBeforeFirstByteReportsNoUsage(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, trialAuthorize, trialUsageLeft)
	started := make(chan struct{})
	upstreamCancelled := make(chan struct{})
	f := newTrialFixture(t, control, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
			return
		}
		close(upstreamCancelled)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	if _, err := f.do(ctx, http.MethodPost, "/v1/chat/completions"); err == nil {
		t.Fatal("expected the cancelled request to fail")
	}
	// Wait for cancellation to reach the upstream. Releasing a response here
	// races cancellation propagation and can legitimately deliver a first byte.
	select {
	case <-upstreamCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("request cancellation did not reach the upstream")
	}
	f.waitUsageSettled(t)
	if got := control.usageCalls.Load(); got != 0 {
		t.Fatalf("usage calls after early cancel = %d", got)
	}
}

func TestTrialStreamCancelledAfterFirstChunkReportsOneUsage(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, trialAuthorize, trialUsageLeft)
	firstChunk := make(chan struct{}, 1)
	release := make(chan struct{})
	close(release)
	f := newTrialFixture(t, control, streamingUpstream(firstChunk, release))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := f.do(ctx, http.MethodPost, "/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	<-firstChunk
	chunk := make([]byte, 64)
	if n, _ := resp.Body.Read(chunk); n == 0 {
		t.Fatal("no streamed bytes reached the client")
	}
	cancel()
	resp.Body.Close()
	f.waitUsageSettled(t)
	if got := control.usageCalls.Load(); got != 1 {
		t.Fatalf("usage calls after late cancel = %d", got)
	}
}

func TestTrialExhaustedByUsageResponseRejectsWithoutUpstream(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, trialAuthorize, trialUsageEmpty)
	f := newTrialFixture(t, control, okUpstream)
	if code, _ := f.status(t, http.MethodPost, "/v1/chat/completions"); code != http.StatusOK {
		t.Fatalf("chat = %d", code)
	}
	f.waitUsageSettled(t)
	before := f.upstreamN.Load()
	code, body := f.status(t, http.MethodPost, "/v1/chat/completions")
	if code != http.StatusPaymentRequired || !strings.Contains(body, `"code":"trial_exhausted"`) {
		t.Fatalf("after exhaustion = %d %s", code, body)
	}
	if !strings.Contains(body, "Your free trial is finished (500 inference requests, 250 MB transfer, or 7 days). Subscribe at https://modeluplink.com/dashboard/ to keep this endpoint online.") {
		t.Fatalf("exhausted body = %s", body)
	}
	if f.upstreamN.Load() != before {
		t.Fatal("exhausted trial request reached the upstream")
	}
	if got := control.usageCalls.Load(); got != 1 {
		t.Fatalf("usage calls = %d", got)
	}
}

func TestTrialLocalDecrementPreventsOvershoot(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, strings.Replace(trialAuthorize, `"trial_remaining":5`, `"trial_remaining":1`, 1), trialUsageEmpty)
	f := newTrialFixture(t, control, okUpstream)
	if code, _ := f.status(t, http.MethodPost, "/v1/embeddings"); code != http.StatusOK {
		t.Fatalf("embeddings = %d", code)
	}
	// The cached authorize (5s TTL) still says one left; the local counter says zero.
	if code, body := f.status(t, http.MethodPost, "/v1/embeddings"); code != http.StatusPaymentRequired || !strings.Contains(body, "trial_exhausted") {
		t.Fatalf("second request = %d %s", code, body)
	}
	f.waitUsageSettled(t)
}

func TestAuthorize402SurfacesTrialCodeAndUpgradeURL(t *testing.T) {
	for _, code := range []string{"trial_exhausted", "trial_expired"} {
		body := fmt.Sprintf(`{"error":{"code":%q,"message":"trial over","upgrade_url":"https://app.example/dashboard/"}}`, code)
		control := newFakeControl(t, http.StatusPaymentRequired, body, `{"trial":false}`)
		f := newTrialFixture(t, control, okUpstream)
		status, got := f.status(t, http.MethodPost, "/v1/chat/completions")
		if status != http.StatusPaymentRequired || !strings.Contains(got, `"code":"`+code+`"`) || !strings.Contains(got, "Subscribe at https://app.example/dashboard/ to keep this endpoint online.") {
			t.Fatalf("%s: %d %s", code, status, got)
		}
		if f.upstreamN.Load() != 0 {
			t.Fatal("402 request reached the upstream")
		}
	}
	control := newFakeControl(t, http.StatusPaymentRequired, `{"error":{"code":"payment_required","message":"pay","upgrade_url":"https://app.example/"}}`, "")
	f := newTrialFixture(t, control, okUpstream)
	if status, got := f.status(t, http.MethodPost, "/v1/chat/completions"); status != http.StatusPaymentRequired || !strings.Contains(got, "billing_inactive") {
		t.Fatalf("payment_required: %d %s", status, got)
	}
}

func TestAuthorizeCacheTTLDependsOnTrial(t *testing.T) {
	control := newFakeControl(t, http.StatusOK, trialAuthorize, "")
	a := newControlAuthorizer(control.server.URL, "ep_1", "agent-token", http.DefaultClient)
	key, _, _ := security.NewToken("mup", "key_1")
	auth, fresh := a.authorize(context.Background(), key)
	if !fresh || !auth.trial || auth.maxConcurrency != 1 || auth.trialRemaining != 5 || auth.trialTransferRemaining != 250_000_000 || auth.trialExpiresAt.IsZero() {
		t.Fatalf("trial authorization = %+v fresh=%v", auth, fresh)
	}
	if _, fresh = a.authorize(context.Background(), key); fresh {
		t.Fatal("second trial authorize was not cached")
	}
	for _, entry := range a.cache {
		if ttl := time.Until(entry.expiresAt); ttl > 5*time.Second || ttl < 4*time.Second {
			t.Fatalf("trial cache ttl = %s", ttl)
		}
	}
	control.set(http.StatusOK, paidAuthorize, "")
	paidKey, _, _ := security.NewToken("mup", "key_2")
	if auth, _ = a.authorize(context.Background(), paidKey); auth.trial || auth.maxConcurrency != 4 {
		t.Fatalf("paid authorization = %+v", auth)
	}
}
