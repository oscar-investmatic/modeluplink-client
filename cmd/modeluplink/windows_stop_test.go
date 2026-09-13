package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
)

func TestWindowsStopPersistsAndUnloadsOnlySharedModel(t *testing.T) {
	if os.Getenv("MODELUPLINK_WINDOWS_TASK_TEST") != "1" {
		t.Skip("requires disposable Windows CI account")
	}
	for _, forUpdate := range []bool{false, true} {
		t.Run(fmt.Sprintf("update-%t", forUpdate), func(t *testing.T) {
			t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
			slug := fmt.Sprintf("ci-stop-%d", os.Getpid())
			loaded := true
			unloads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/ps":
					models := []map[string]string{{"name": "independent:latest"}}
					if loaded {
						models = append(models, map[string]string{"name": "selected:latest"})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
				case "/api/generate":
					var body struct {
						Model     string `json:"model"`
						KeepAlive int    `json:"keep_alive"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "selected:latest" || body.KeepAlive != 0 {
						t.Error("unloaded independent model or invalid request")
					}
					path, _ := localconfig.EndpointPath(slug)
					saved, err := localconfig.LoadEndpoint(path)
					if err != nil || !saved.Stopped || !saved.MemoryReleasePending {
						t.Error("stop was not durable before unload")
					}
					loaded = false
					unloads++
					_, _ = w.Write([]byte(`{"done":true}`))
				default:
					t.Error("unexpected request")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			cfg, _ := localconfig.Load()
			e := localconfig.Endpoint{Slug: slug, Engine: "ollama", UpstreamURL: server.URL, SharedModels: []string{"selected:latest"}}
			cfg.Endpoints[slug] = e
			cfg.UpgradeResume = []string{slug}
			if err := localconfig.Save(cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := localconfig.SaveEndpoint(e); err != nil {
				t.Fatal(err)
			}
			reply, err := stopSharingForMaintenance(cfg, e, nil, forUpdate)
			if err != nil || reply.MemoryReleasePending[slug] || !reply.Stopped[slug] || unloads != 1 {
				t.Fatalf("stop: %v %+v", err, reply)
			}
			cfg, err = localconfig.Load()
			if err != nil {
				t.Fatal(err)
			}
			if contains(cfg.UpgradeResume, slug) != forUpdate {
				t.Fatal("explicit Stop did not cancel upgrade resume, or update lost its resume state")
			}
			// An explicit second Stop clears a prepared update and does not unload a model
			// that another application might have loaded since the previous Stop.
			loaded = true
			if _, err := desktopStopSharing(cfg, cfg.Endpoints[slug], nil); err != nil {
				t.Fatal(err)
			}
			cfg, err = localconfig.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.UpgradeResume) != 0 || unloads != 1 {
				t.Fatal("second Stop changed independent memory or retained automatic resume")
			}
		})
	}
}
func TestWindowsStopMakesUnconfirmedReleaseVisible(t *testing.T) {
	if os.Getenv("MODELUPLINK_WINDOWS_TASK_TEST") != "1" {
		t.Skip("requires disposable Windows CI account")
	}
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	cfg, _ := localconfig.Load()
	e := localconfig.Endpoint{Slug: fmt.Sprintf("ci-pending-%d", os.Getpid()), Engine: "ollama", UpstreamURL: server.URL, SharedModels: []string{"selected:latest"}}
	cfg.Endpoints[e.Slug] = e
	reply, err := desktopStopSharing(cfg, e, nil)
	if err != nil || !reply.MemoryReleasePending[e.Slug] || reply.Notice == "" {
		t.Fatalf("release failure hidden: %v %+v", err, reply)
	}
	path, _ := localconfig.EndpointPath(e.Slug)
	saved, err := localconfig.LoadEndpoint(path)
	if err != nil || !saved.Stopped || !saved.MemoryReleasePending {
		t.Fatal("stopped/pending state not persisted")
	}
}
