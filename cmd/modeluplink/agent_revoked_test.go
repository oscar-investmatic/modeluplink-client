package main

import (
	"context"
	"encoding/json"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRevokedConnectionUnloadsBeforeRemovingAutoStart(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "released", true: "pending"}[fail], func(t *testing.T) {
			t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
			loaded := true
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/ps" {
					if loaded {
						w.Write([]byte(`{"models":[{"name":"llama3.2:3b"}]}`))
					} else {
						w.Write([]byte(`{"models":[]}`))
					}
					return
				}
				if r.URL.Path != "/api/generate" {
					t.Errorf("unexpected %s", r.URL.Path)
					return
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["model"] != "llama3.2:3b" || body["keep_alive"] != float64(0) {
					t.Error("not an unload request")
				}
				if fail {
					w.WriteHeader(503)
					return
				}
				loaded = false
				w.Write([]byte(`{"done":true}`))
			}))
			defer server.Close()
			e := localconfig.Endpoint{ID: "ep_old", Slug: "try-old", Engine: "ollama", UpstreamURL: server.URL, SharedModels: []string{"llama3.2:3b"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := retireCurrentService
			t.Cleanup(func() { retireCurrentService = original })
			retired := false
			retireCurrentService = func(slug string) error {
				savedPath, _ := localconfig.EndpointPath(slug)
				saved, err := localconfig.LoadEndpoint(savedPath)
				if err != nil || !saved.Revoked || !saved.Stopped || saved.MemoryReleasePending != fail {
					t.Errorf("wrong saved state %+v %v", saved, err)
				}
				if loaded != fail {
					t.Error("service removed before model unload")
				}
				retired = true
				cancel()
				return nil
			}
			if err := retireRevokedEndpoint(ctx, e); err != nil || !retired {
				t.Fatalf("retire: %v", err)
			}
		})
	}
}

func TestRevokingAttachedOllamaDoesNotTouchItsRuntime(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("attached runtime contacted during retirement") }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := retireCurrentService
	defer func() { retireCurrentService = original }()
	retireCurrentService = func(string) error { cancel(); return nil }
	e := localconfig.Endpoint{ID: "external", Slug: "friend-gpu", Engine: "ollama", RuntimeOwnership: "external", UpstreamURL: server.URL, SharedModels: []string{"selected-model"}}
	if err := retireRevokedEndpoint(ctx, e); err != nil {
		t.Fatal(err)
	}
	path, _ := localconfig.EndpointPath(e.Slug)
	saved, err := localconfig.LoadEndpoint(path)
	if err != nil || !saved.Stopped || !saved.Revoked || saved.MemoryReleasePending {
		t.Fatal("incorrect retirement state")
	}
}
