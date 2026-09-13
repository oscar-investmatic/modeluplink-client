package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBaseBoundary(t *testing.T) {
	for _, tt := range []struct {
		raw, want string
		lan       bool
	}{
		{"http://localhost:1234/", "http://127.0.0.1:1234/v1", false},
		{"http://[::1]:8080/v1/", "http://[::1]:8080/v1", false},
		{"http://127.0.0.1:1337/jan/v1", "http://127.0.0.1:1337/jan/v1", false},
		{"https://192.168.1.10:8000", "https://192.168.1.10:8000/v1", true},
	} {
		got, err := Base(tt.raw, tt.lan)
		if err != nil || got != tt.want {
			t.Fatalf("%s => %s %v", tt.raw, got, err)
		}
	}
	for _, raw := range []string{"https://example.com/v1", "http://169.254.169.254/v1", "http://100.100.100.200/v1", "http://0.0.0.0:8000", "file:///etc/passwd", "http://user:secret@127.0.0.1/v1", "http://127.0.0.1:1234/chat", "http://127.0.0.1/v1/chat/completions", "http://127.0.0.1/v1?key=secret", "http://127.0.0.1/%2e%2e/v1", "http://127.0.0.1/a/../v1"} {
		if _, err := Base(raw, true); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}
func TestAuthenticatedDiscoveryAndReadiness(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-secret" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/prefix/v1/models":
			w.Write([]byte(`{"data":[{"id":"org/model:q4"},{"id":"org/model:q4"}]}`))
		case "/prefix/v1/chat/completions":
			posts.Add(1)
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			if b["max_tokens"].(float64) < 128 {
				w.Write([]byte(`{"choices":[{"finish_reason":"length","message":{"content":"","reasoning_content":"still thinking"}}]}`))
				return
			}
			if b["model"] != "org/model:q4" {
				t.Error("model ID changed")
			}
			w.Write([]byte(`{"choices":[{"message":{"content":"ready"}}]}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := Connection{URL: server.URL + "/prefix/v1", Key: "local-secret"}
	models, err := c.Models(context.Background())
	if err != nil || len(models) != 1 || posts.Load() != 0 {
		t.Fatalf("discovery %v %v posts %d", models, err, posts.Load())
	}
	if err = c.Test(context.Background(), models[0]); err != nil || posts.Load() != 1 {
		t.Fatalf("test %v", err)
	}
	c.Key = "wrong"
	_, err = c.Models(context.Background())
	if Code(err) != "authentication_required" || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("error %v", err)
	}
}
func TestDiscoveryNeverFollowsRedirectsOrAcceptsChatPages(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	_, err := (Connection{URL: redirect.URL, Key: "secret"}).Models(context.Background())
	if err == nil || hits.Load() != 0 {
		t.Fatal("redirect followed")
	}
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html>chat UI</html>`)) }))
	defer html.Close()
	if _, err = (Connection{URL: html.URL}).Models(context.Background()); Code(err) != "invalid_response" {
		t.Fatalf("accepted chat UI %v", err)
	}
}
