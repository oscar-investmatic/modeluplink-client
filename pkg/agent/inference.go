package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
	mtunnel "github.com/oscar-investmatic/modeluplink-client/pkg/tunnel"
	"github.com/oscar-investmatic/modeluplink-client/internal/upstream"
)

const (
	maxInferenceBody = 32 << 20
	// rejectedBodyDrain bounds how much of a rejected request's declared body
	// the agent reads so the TLS session stays reusable; larger or unsized
	// bodies are left to net/http, which closes the connection after replying.
	rejectedBodyDrain = 1 << 20
	// paidConcurrency is the in-flight request limit before the control plane
	// has reported one; trial endpoints are limited to one.
	paidConcurrency    = 4
	usageReportTimeout = 5 * time.Second
)

type localInference struct {
	closing        bool
	requests       sync.WaitGroup
	usageDone      chan struct{}
	config         Config
	client         *http.Client
	upstreamClient *http.Client
	authorizer     *controlAuthorizer
	mu             sync.Mutex
	keyStarts      map[string][]time.Time
	// inFlight and maxConcurrency replace a fixed semaphore so that the limit
	// reported by authorize (1 on trial, 4 paid) can change between requests.
	inFlight       int
	maxConcurrency int
	// trialRemaining is the local view of the allowance: -1 when the endpoint
	// is paid or the trial state is unknown. It is decremented as soon as a
	// request completes so concurrent requests cannot overshoot, and re-synced
	// from every fresh authorize and usage response minus pendingUsage, the
	// completions not yet acknowledged by the control plane.
	trialRemaining         int
	trialTransferRemaining int64
	pendingUsage           int
	pendingTransferBytes   int64
	trialCode              string
	upgradeURL             string
	usageQueued            int
	usageTransferQueued    int64
	usageEventsQueued      int
	usageRunning           bool
}

func newLocalInference(config Config, client *http.Client) *localInference {
	return &localInference{
		config:                 config,
		client:                 client,
		upstreamClient:         upstream.Client(0),
		authorizer:             newControlAuthorizer(config.ControlURL, config.EndpointID, config.AgentToken, client),
		keyStarts:              make(map[string][]time.Time),
		maxConcurrency:         paidConcurrency,
		trialRemaining:         -1,
		trialTransferRemaining: -1,
	}
}

func (h *localInference) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		http.Error(w, "sharing stopped", http.StatusServiceUnavailable)
		return
	}
	h.requests.Add(1)
	h.mu.Unlock()
	defer h.requests.Done()
	origin := r.Header.Get("Origin")
	if r.Method == http.MethodOptions {
		h.preflight(w, r, origin)
		return
	}
	if origin != "" {
		if !h.originAllowed(origin) {
			h.reject(w, r, http.StatusForbidden, "origin_not_allowed", "browser origin is not allowed for this endpoint")
			return
		}
		h.corsHeaders(w, origin)
	}
	if !allowed(r.Method, r.URL.Path) {
		h.reject(w, r, http.StatusNotFound, "route_not_found", "this endpoint is not exposed by Model Uplink")
		return
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		h.reject(w, r, http.StatusUnauthorized, "invalid_api_key", "invalid or revoked API key")
		return
	}
	plainKey := strings.TrimPrefix(header, "Bearer ")
	auth, fresh := h.authorizer.authorize(r.Context(), plainKey)
	if auth.status != http.StatusOK {
		switch {
		case auth.status == http.StatusPaymentRequired && isTrialCode(auth.code):
			h.mu.Lock()
			h.trialCode, h.upgradeURL = auth.code, auth.upgradeURL
			h.mu.Unlock()
			h.rejectTrial(w, r, auth.code, auth.upgradeURL)
		case auth.status == http.StatusPaymentRequired:
			h.reject(w, r, auth.status, "billing_inactive", "endpoint subscription is inactive")
		case auth.status == http.StatusServiceUnavailable:
			h.reject(w, r, auth.status, "control_unavailable", "API key could not be validated")
		default:
			h.reject(w, r, http.StatusUnauthorized, "invalid_api_key", "invalid or revoked API key")
		}
		return
	}
	if !h.allowKey(auth.keyID, time.Now()) {
		h.reject(w, r, http.StatusTooManyRequests, "rate_limit_exceeded", "API key rate limit exceeded")
		return
	}
	model, valid := h.checkModel(w, r)
	if !valid {
		return
	}
	if r.Method == http.MethodPost && h.config.scheduler != nil {
		release, err := h.config.scheduler.acquireForKey(r.Context(), model, auth.keyID)
		if err != nil {
			w.Header().Set("Retry-After", "5")
			h.reject(w, r, 429, "model_busy", "the model is busy; please retry shortly")
			return
		}
		defer release()
		// Refresh authorization after waiting: billing or key access may have changed.
		auth, fresh = h.authorizer.authorize(r.Context(), plainKey)
		if auth.status != http.StatusOK {
			h.reject(w, r, auth.status, "authorization_failed", "request is no longer authorized")
			return
		}
	}
	trial, code, upgradeURL, admitted := h.admit(auth, fresh)
	if trial && code != "" {
		h.rejectTrial(w, r, code, upgradeURL)
		return
	}
	if !admitted {
		h.reject(w, r, http.StatusTooManyRequests, "concurrency_limit_exceeded", "endpoint concurrency limit exceeded")
		return
	}
	defer h.release()
	if completed, successful, transferBytes := h.proxy(w, r); successful {
		// Model discovery starts the clock because it is a successful API call,
		// but does not consume either metered allowance.
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			transferBytes = 0
		}
		h.recordUsage(completed, transferBytes)
	}
}

// admit syncs the endpoint's trial state from the authorization and takes a
// concurrency slot. It returns the trial rejection code when the allowance is
// finished, and admitted=false when the endpoint is at its concurrency limit.
func (h *localInference) admit(auth authorization, fresh bool) (trial bool, code, upgradeURL string, admitted bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.maxConcurrency = auth.maxConcurrency
	if auth.trial {
		if auth.upgradeURL != "" {
			h.upgradeURL = auth.upgradeURL
		}
		if fresh || h.trialRemaining < 0 {
			h.trialRemaining = auth.trialRemaining - h.pendingUsage
			h.trialTransferRemaining = auth.trialTransferRemaining - h.pendingTransferBytes
			h.trialCode = ""
		}
		if h.trialRemaining <= 0 || h.trialTransferRemaining <= 0 {
			code = h.trialCode
			if code == "" {
				code = "trial_exhausted"
				if !auth.trialExpiresAt.IsZero() && !time.Now().Before(auth.trialExpiresAt) {
					code = "trial_expired"
				}
			}
			return true, code, h.upgradeURL, false
		}
	} else if fresh {
		h.trialRemaining, h.trialTransferRemaining, h.trialCode = -1, -1, ""
	}
	if h.inFlight >= h.maxConcurrency {
		return auth.trial, "", "", false
	}
	h.inFlight++
	return auth.trial, "", "", true
}

func (h *localInference) release() {
	h.mu.Lock()
	h.inFlight--
	h.mu.Unlock()
}

// recordUsage queues privacy-preserving activity for both paid and trial
// endpoints. Trial endpoints also reserve their local allowance until the
// control plane acknowledges the report. Completed is 1 for inference calls
// and 0 for model discovery.
func (h *localInference) recordUsage(completed int, transferBytes int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.trialRemaining >= 0 {
		h.trialRemaining -= completed
		h.trialTransferRemaining -= transferBytes
		h.pendingUsage += completed
		h.pendingTransferBytes += transferBytes
	}
	h.usageQueued += completed
	h.usageTransferQueued += transferBytes
	h.usageEventsQueued++
	if !h.usageRunning {
		h.usageRunning = true
		done := make(chan struct{})
		h.usageDone = done
		go func() { defer close(done); h.reportUsage() }()
	}
}

func (h *localInference) reportUsage() {
	for {
		h.mu.Lock()
		if h.usageEventsQueued == 0 {
			h.usageRunning = false
			h.mu.Unlock()
			return
		}
		count := h.usageQueued
		transferBytes := h.usageTransferQueued
		h.usageQueued = 0
		h.usageTransferQueued = 0
		h.usageEventsQueued = 0
		h.mu.Unlock()
		result, err := h.authorizer.usage(count, transferBytes)
		h.mu.Lock()
		if err != nil {
			// Keep unacknowledged usage in both the local allowance and the
			// queue. The next successful call retries the combined report.
			h.usageQueued += count
			h.usageTransferQueued += transferBytes
			h.usageEventsQueued++
			h.usageRunning = false
			h.mu.Unlock()
			return
		}
		h.pendingUsage = max(0, h.pendingUsage-count)
		h.pendingTransferBytes = max(0, h.pendingTransferBytes-transferBytes)
		if result.Trial {
			h.trialRemaining = result.TrialRemaining - h.pendingUsage
			h.trialTransferRemaining = result.TrialTransferRemaining - h.pendingTransferBytes
			if isTrialCode("trial_" + result.TrialStatus) {
				h.trialCode = "trial_" + result.TrialStatus
			}
		} else {
			h.trialRemaining, h.trialTransferRemaining, h.trialCode = -1, -1, ""
		}
		h.mu.Unlock()
	}
}

func (h *localInference) preflight(w http.ResponseWriter, r *http.Request, origin string) {
	requestedMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
	if origin == "" || !h.originAllowed(origin) {
		h.writeError(w, http.StatusForbidden, "origin_not_allowed", "browser origin is not allowed for this endpoint")
		return
	}
	if !allowed(requestedMethod, r.URL.Path) {
		h.writeError(w, http.StatusNotFound, "route_not_found", "this endpoint is not exposed by Model Uplink")
		return
	}
	for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
		header = strings.ToLower(strings.TrimSpace(header))
		if header != "" && header != "authorization" && header != "content-type" {
			h.writeError(w, http.StatusForbidden, "header_not_allowed", "browser request header is not allowed")
			return
		}
	}
	h.corsHeaders(w, origin)
	w.Header().Set("Access-Control-Allow-Methods", requestedMethod)
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func (h *localInference) originAllowed(raw string) bool {
	origin, err := security.NormalizeCORSOrigin(raw)
	if err != nil {
		return false
	}
	for _, allowedOrigin := range h.config.CORSOrigins {
		if origin == allowedOrigin {
			return true
		}
	}
	return false
}

func (h *localInference) corsHeaders(w http.ResponseWriter, origin string) {
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
}

func (h *localInference) allowKey(id string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	starts := h.keyStarts[id]
	first := 0
	for first < len(starts) && starts[first].Before(cutoff) {
		first++
	}
	starts = starts[first:]
	if len(starts) >= 60 {
		h.keyStarts[id] = starts
		return false
	}
	h.keyStarts[id] = append(starts, now)
	return true
}

// proxy forwards the request to the local upstream. A call is successful once
// a 2xx response has delivered at least one byte to the client. completed is 1
// for successful inference POSTs and 0 for successful model discovery.
func (h *localInference) proxy(w http.ResponseWriter, r *http.Request) (completed int, successful bool, transferBytes int64) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	rawBase := strings.TrimRight(h.config.UpstreamURL, "/")
	if h.config.Engine != "openai_compatible" && !strings.HasSuffix(rawBase, "/v1") {
		rawBase += "/v1"
	}
	base, baseErr := upstream.Base(rawBase, h.config.AllowLAN)
	if baseErr != nil {
		h.writeError(w, 502, "upstream_error", "invalid local API configuration")
		return 0, false, 0
	}
	target := upstream.Route(base, r.URL.Path)
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	body := &byteCountingReader{Reader: http.MaxBytesReader(w, r.Body, maxInferenceBody)}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, body)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "request could not be forwarded")
		return 0, false, 0
	}
	mtunnel.ApplyHeaders(req.Header, mtunnel.RequestHeaders(r.Header))
	if h.config.UpstreamKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.config.UpstreamKey)
	}
	req.Host = ""
	resp, err := h.upstreamClient.Do(req)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request exceeds 32 MiB")
			return 0, false, 0
		}
		h.writeError(w, http.StatusBadGateway, "upstream_error", "local upstream request failed")
		return 0, false, 0
	}
	defer resp.Body.Close()
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" && (h.config.SharedModels != nil || h.config.Engine == "openai_compatible") && resp.StatusCode == http.StatusOK {
		outcome := &responseOutcome{ResponseWriter: w}
		h.modelList(outcome, resp)
		return 0, outcome.status >= 200 && outcome.status < 300 && outcome.bytes > 0, outcome.bytes
	}
	mtunnel.ApplyHeaders(w.Header(), mtunnel.ResponseHeaders(resp.Header))
	w.WriteHeader(resp.StatusCode)
	countable := r.Method == http.MethodPost && resp.StatusCode >= 200 && resp.StatusCode < 300
	successStatus := resp.StatusCode >= 200 && resp.StatusCode < 300
	limited := io.LimitReader(resp.Body, maxInferenceBody+1)
	buf := make([]byte, mtunnel.ChunkSize)
	var written, delivered int64
	for {
		n, readErr := limited.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxInferenceBody {
				return completed, successful, body.n + delivered
			}
			if _, err = w.Write(buf[:n]); err != nil {
				return completed, successful, body.n + delivered
			}
			delivered += int64(n)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			successful = successStatus
			if countable {
				completed = 1
			}
		}
		if readErr != nil {
			return completed, successful, body.n + delivered
		}
	}
}

type byteCountingReader struct {
	io.Reader
	n int64
}

type responseOutcome struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseOutcome) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseOutcome) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (r *byteCountingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += int64(n)
	return n, err
}

// reject answers a request that will not be proxied. Small declared bodies
// are drained first so that a rejected POST does not force net/http to close
// the client's TLS session (and, with it, the multiplexed stream).
func (h *localInference) reject(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if r.ContentLength > 0 && r.ContentLength <= rejectedBodyDrain {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, rejectedBodyDrain))
	}
	h.writeError(w, status, code, message)
}

func (h *localInference) rejectTrial(w http.ResponseWriter, r *http.Request, code, upgradeURL string) {
	if upgradeURL == "" {
		upgradeURL = "https://modeluplink.com/dashboard/"
	}
	h.reject(w, r, http.StatusPaymentRequired, code, "Your free trial is finished (500 inference requests, 250 MB transfer, or 7 days). Subscribe at "+upgradeURL+" to keep this endpoint online.")
}

func isTrialCode(code string) bool { return code == "trial_exhausted" || code == "trial_expired" }

func (h *localInference) writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message, "type": "modeluplink_error", "code": code}})
}

// authorization is the control plane's answer for one API key.
type authorization struct {
	keyID                  string
	status                 int
	trial                  bool
	maxConcurrency         int
	trialRemaining         int
	trialTransferRemaining int64
	trialExpiresAt         time.Time
	// code and upgradeURL come from a 402 body.
	code, upgradeURL string
}

type authorizationCacheEntry struct {
	authorization
	expiresAt time.Time
}

type usageResult struct {
	Trial                  bool   `json:"trial"`
	TrialRemaining         int    `json:"trial_remaining"`
	TrialTransferRemaining int64  `json:"trial_transfer_remaining"`
	TrialStatus            string `json:"trial_status"`
}

type controlAuthorizer struct {
	url, endpointID, agentToken string
	client                      *http.Client
	mu                          sync.Mutex
	cache                       map[[32]byte]authorizationCacheEntry
}

func newControlAuthorizer(url, endpointID, agentToken string, client *http.Client) *controlAuthorizer {
	return &controlAuthorizer{url: strings.TrimRight(url, "/"), endpointID: endpointID, agentToken: agentToken, client: client, cache: make(map[[32]byte]authorizationCacheEntry)}
}

// authorize returns the cached or freshly fetched authorization for a key;
// fresh is false when the answer came from the cache.
func (a *controlAuthorizer) authorize(ctx context.Context, plainKey string) (auth authorization, fresh bool) {
	hash := sha256.Sum256([]byte(plainKey))
	now := time.Now()
	a.mu.Lock()
	entry, found := a.cache[hash]
	a.mu.Unlock()
	if found && now.Before(entry.expiresAt) {
		return entry.authorization, false
	}
	keyID, secret, err := security.ParseToken(plainKey, "mup")
	if err != nil {
		return authorization{status: http.StatusUnauthorized}, true
	}
	payload, _ := json.Marshal(map[string]string{"endpoint_id": a.endpointID, "key_id": keyID, "proof": security.InferenceProof(keyID, secret)})
	resp, err := a.post(ctx, "/v1/agent/authorize", payload)
	if err != nil {
		return authorization{status: http.StatusServiceUnavailable}, true
	}
	defer resp.Body.Close()
	auth = authorization{status: resp.StatusCode, maxConcurrency: paidConcurrency}
	ttl := 55 * time.Second
	switch auth.status {
	case http.StatusOK:
		var output struct {
			KeyID                  string `json:"key_id"`
			Trial                  bool   `json:"trial"`
			MaxConcurrency         int    `json:"max_concurrency"`
			TrialRemaining         int    `json:"trial_remaining"`
			TrialTransferRemaining int64  `json:"trial_transfer_remaining"`
			TrialExpiresAt         string `json:"trial_expires_at"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&output) != nil || output.KeyID == "" {
			auth.status = http.StatusServiceUnavailable
			break
		}
		auth.keyID, auth.trial, auth.trialRemaining = output.KeyID, output.Trial, output.TrialRemaining
		auth.trialTransferRemaining = output.TrialTransferRemaining
		if output.MaxConcurrency > 0 {
			auth.maxConcurrency = output.MaxConcurrency
		} else if output.Trial {
			auth.maxConcurrency = 1
		}
		if output.Trial {
			auth.trialExpiresAt, _ = time.Parse(time.RFC3339, output.TrialExpiresAt)
			ttl = 5 * time.Second
		}
	case http.StatusPaymentRequired:
		var output struct {
			Error struct {
				Code       string `json:"code"`
				UpgradeURL string `json:"upgrade_url"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&output)
		auth.code, auth.upgradeURL = output.Error.Code, output.Error.UpgradeURL
		if isTrialCode(auth.code) {
			ttl = 5 * time.Second
		}
	case http.StatusUnauthorized:
	default:
		auth.status = http.StatusServiceUnavailable
	}
	if auth.status == http.StatusServiceUnavailable {
		ttl = 2 * time.Second
	}
	a.mu.Lock()
	a.cache[hash] = authorizationCacheEntry{authorization: auth, expiresAt: now.Add(ttl)}
	a.mu.Unlock()
	return auth, true
}

// usage reports completed trial requests to the control plane.
func (a *controlAuthorizer) usage(completed int, transferBytes int64) (usageResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), usageReportTimeout)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{"endpoint_id": a.endpointID, "completed": completed, "transfer_bytes": transferBytes})
	resp, err := a.post(ctx, "/v1/agent/usage", payload)
	if err != nil {
		return usageResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return usageResult{}, errors.New("usage report returned " + resp.Status)
	}
	var result usageResult
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&result); err != nil {
		return usageResult{}, err
	}
	return result, nil
}

func (a *controlAuthorizer) post(ctx context.Context, path string, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.agentToken)
	req.Header.Set("Content-Type", "application/json")
	return a.client.Do(req)
}
