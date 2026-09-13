package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckModelRequiresRealCompletionAndUnloads(t *testing.T) {
	// Qwen 3 on Ollama: the single token is the swallowed think tag, so the
	// only evidence of a completed generation is done_reason and eval_count.
	qwen := `{"model":"qwen3:8b","response":"","done":true,"done_reason":"length","eval_count":1}`
	passing := map[string]bool{`{"response":"O","done":true}`: true, `{"response":"","thinking":"Okay","done":true}`: true, qwen: true}
	for _, body := range []string{`{"response":"O","done":true}`, `{"response":"","thinking":"Okay","done":true}`, qwen, `{"response":"","done":true}`, `{"response":"","thinking":"","done":true}`, `{"response":"O","done":false}`, `{"error":"not enough memory"}`, `{"response":"","done":true,"done_reason":"length","error":"boom"}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				json.NewDecoder(r.Body).Decode(&request)
				if r.URL.Path != "/api/generate" || request["keep_alive"] != float64(0) || request["model"] != "small:1" {
					t.Error("incorrect readiness request")
				}
				w.Write([]byte(body))
			}))
			defer server.Close()
			err := CheckModel(context.Background(), server.URL, "small:1")
			if (err == nil) != passing[body] {
				t.Fatalf("unexpected readiness: %v", err)
			}
		})
	}
}
