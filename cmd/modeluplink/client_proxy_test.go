package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVerifiedClientProxyRequiresPinAndStreamsOpenAIRequest(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer remote-secret" {
			t.Errorf("upstream authorization = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	certificate := upstream.Certificate()
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	digest := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
	pin := "sha256/" + base64.StdEncoding.EncodeToString(digest[:])

	handler, err := newClientProxyHandler(clientCredentials{Endpoint: upstream.URL + "/v1", APIKey: "remote-secret", TLSPin: pin}, roots)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer local-placeholder")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "data: first\n\ndata: [DONE]\n\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}

	wrongDigest := sha256.Sum256([]byte("wrong endpoint"))
	wrongPin := "sha256/" + base64.StdEncoding.EncodeToString(wrongDigest[:])
	wrongHandler, err := newClientProxyHandler(clientCredentials{Endpoint: upstream.URL + "/v1", APIKey: "remote-secret", TLSPin: wrongPin}, roots)
	if err != nil {
		t.Fatal(err)
	}
	wrongResponse := httptest.NewRecorder()
	wrongHandler.ServeHTTP(wrongResponse, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if wrongResponse.Code != http.StatusBadGateway {
		t.Fatalf("wrong pin status=%d body=%s", wrongResponse.Code, wrongResponse.Body.String())
	}

	untrustedHandler, err := newClientProxyHandler(clientCredentials{Endpoint: upstream.URL + "/v1", APIKey: "remote-secret", TLSPin: pin}, x509.NewCertPool())
	if err != nil {
		t.Fatal(err)
	}
	untrustedResponse := httptest.NewRecorder()
	untrustedHandler.ServeHTTP(untrustedResponse, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if untrustedResponse.Code != http.StatusBadGateway {
		t.Fatalf("untrusted chain status=%d body=%s", untrustedResponse.Code, untrustedResponse.Body.String())
	}
}

func TestVerifiedClientProxyRejectsUnsafeLocalSurface(t *testing.T) {
	if err := validateLoopbackListen("0.0.0.0:11435"); err == nil {
		t.Fatal("non-loopback listener was accepted")
	}
	if err := validateLoopbackListen("127.0.0.1:11435"); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("pin"))
	pin := "sha256/" + base64.StdEncoding.EncodeToString(digest[:])
	handler, err := newClientProxyHandler(clientCredentials{Endpoint: "https://example.com/v1", APIKey: "remote-secret", TLSPin: pin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, httptest.NewRequest(http.MethodPost, "/api/pull", nil))
	if adminResponse.Code != http.StatusNotFound {
		t.Fatalf("administrative route status=%d", adminResponse.Code)
	}
	browserRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	browserRequest.Header.Set("Origin", "https://malicious.example")
	browserResponse := httptest.NewRecorder()
	handler.ServeHTTP(browserResponse, browserRequest)
	if browserResponse.Code != http.StatusForbidden {
		t.Fatalf("browser origin status=%d", browserResponse.Code)
	}
}

func TestClientCredentialsRequirePrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	contents := `{"endpoint":"https://home-gpu.modeluplink.test/v1","api_key":"secret","tls_pin":"sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}`
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadClientCredentials(path); err == nil {
		t.Fatal("world-readable credentials were accepted")
	}
	if runtime.GOOS == "windows" {
		return
	} // Plain JSON must remain rejected; DPAPI round-trip is tested below.
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	credentials, err := loadClientCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(credentials.Endpoint)
	if parsed.Hostname() != "home-gpu.modeluplink.test" {
		t.Fatalf("endpoint = %q", credentials.Endpoint)
	}
}

func TestSaveClientCredentialsIsPrivateAndNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	want := clientCredentials{Endpoint: "https://home-gpu.modeluplink.test/v1", APIKey: "secret", TLSPin: "sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}
	if err := saveClientCredentials(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0600 {
		t.Fatalf("credentials mode = %o", got)
	}
	if err := saveClientCredentials(path, clientCredentials{APIKey: "replacement"}); err == nil {
		t.Fatal("existing credentials file was overwritten")
	}
	got, err := loadClientCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("credentials = %#v", got)
	}
}
