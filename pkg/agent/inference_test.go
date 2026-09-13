package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyAcceptsVersionedUpstreamURL(t *testing.T) {
	for _, basePath := range []string{"", "/", "/v1", "/v1/", "/proxy", "/proxy/v1/"} {
		t.Run(basePath, func(t *testing.T) {
			prefix := ""
			if basePath == "/proxy" || basePath == "/proxy/v1/" {
				prefix = "/proxy"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.RequestURI != prefix+"/v1/chat/completions?test=one%20two" {
					t.Errorf("upstream URI = %q", r.RequestURI)
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(`{"choices":[]}`))
			}))
			defer upstream.Close()
			h := newLocalInference(Config{UpstreamURL: upstream.URL + basePath}, upstream.Client())
			rec := httptest.NewRecorder()
			completed, successful, _ := h.proxy(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions?test=one%20two", nil))
			if rec.Code != http.StatusOK || !successful || completed != 1 {
				t.Fatalf("status=%d successful=%v completed=%d", rec.Code, successful, completed)
			}
		})
	}
}

func TestCORSPreflightUsesExactAllowlist(t *testing.T) {
	handler := newLocalInference(Config{CORSOrigins: []string{"https://editor.example"}}, http.DefaultClient)

	allowed := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	allowed.Header.Set("Origin", "https://editor.example")
	allowed.Header.Set("Access-Control-Request-Method", http.MethodPost)
	allowed.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusNoContent {
		t.Fatalf("allowed preflight status = %d", allowedResponse.Code)
	}
	if got := allowedResponse.Header().Get("Access-Control-Allow-Origin"); got != "https://editor.example" {
		t.Fatalf("allow origin = %q", got)
	}

	blocked := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	blocked.Header.Set("Origin", "https://other.example")
	blocked.Header.Set("Access-Control-Request-Method", http.MethodPost)
	blockedResponse := httptest.NewRecorder()
	handler.ServeHTTP(blockedResponse, blocked)
	if blockedResponse.Code != http.StatusForbidden {
		t.Fatalf("blocked preflight status = %d", blockedResponse.Code)
	}
}

func TestCORSPreflightCannotExposeAdministrativeRouteOrHeader(t *testing.T) {
	handler := newLocalInference(Config{CORSOrigins: []string{"https://editor.example"}}, http.DefaultClient)

	admin := httptest.NewRequest(http.MethodOptions, "/api/pull", nil)
	admin.Header.Set("Origin", "https://editor.example")
	admin.Header.Set("Access-Control-Request-Method", http.MethodPost)
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, admin)
	if adminResponse.Code != http.StatusNotFound {
		t.Fatalf("administrative preflight status = %d", adminResponse.Code)
	}

	customHeader := httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
	customHeader.Header.Set("Origin", "https://editor.example")
	customHeader.Header.Set("Access-Control-Request-Method", http.MethodGet)
	customHeader.Header.Set("Access-Control-Request-Headers", "X-Unsafe")
	customHeaderResponse := httptest.NewRecorder()
	handler.ServeHTTP(customHeaderResponse, customHeader)
	if customHeaderResponse.Code != http.StatusForbidden {
		t.Fatalf("custom-header preflight status = %d", customHeaderResponse.Code)
	}
}
