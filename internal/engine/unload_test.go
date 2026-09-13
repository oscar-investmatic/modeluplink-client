package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUnloadChecksMemoryAndLeavesUnsharedModels(t *testing.T) {
	loaded := true
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ps" {
			polls++
			models := []map[string]string{{"name": "private:1"}}
			if loaded {
				models = append(models, map[string]string{"name": "shared:1"})
			}
			json.NewEncoder(w).Encode(map[string]any{"models": models})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "shared:1" || body["keep_alive"] != float64(0) || body["prompt"] != nil {
			t.Error("unloaded wrong model or generated text")
		}
		loaded = false
		w.Write([]byte(`{"done":true}`))
	}))
	defer server.Close()
	if err := UnloadModels(context.Background(), server.URL, []string{"shared:1"}); err != nil {
		t.Fatal(err)
	}
	if loaded || polls < 2 {
		t.Fatal("did not confirm release")
	}
}
func TestUnloadDoesNotClaimSuccessForUnknownOrStillLoadedModels(t *testing.T) {
	for _, body := range []string{`{}`, `{"models":[{"name":"shared:1"}]}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/generate" {
					w.Write([]byte(`{"done":true}`))
					return
				}
				w.Write([]byte(body))
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if err := UnloadModels(ctx, server.URL, []string{"shared:1"}); err == nil {
				t.Fatal("claimed memory was released")
			}
		})
	}
}
