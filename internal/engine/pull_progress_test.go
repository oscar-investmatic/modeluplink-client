package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPullProgressArrivesBeforeCompletion(t *testing.T) {
	received := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/pull" {
			t.Error("wrong pull request")
		}
		fmt.Fprintln(w, `{"status":"pulling abc","digest":"abc","completed":25,"total":100}`)
		w.(http.Flusher).Flush()
		select {
		case <-received:
		case <-r.Context().Done():
			return
		}
		fmt.Fprintln(w, `{"status":"verifying sha256 digest"}`)
		fmt.Fprintln(w, `{"status":"success"}`)
	}))
	defer server.Close()
	count := 0
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := PullModelWithProgress(ctx, server.URL, "test", func(p PullProgress) {
		count++
		if count == 1 {
			if p.Completed != 25 || p.Total != 100 {
				t.Error("lost actual bytes")
			}
			close(received)
		}
	})
	if err != nil || count != 3 {
		t.Fatalf("events=%d err=%v", count, err)
	}
}
func TestPullProgressRejectsIncompleteAndFailedStreams(t *testing.T) {
	for _, body := range []string{
		`{"status":"pulling abc","completed":100,"total":100}`,
		`{"error":"no space left on device"}`,
		`{"status":"pulling abc","completed":101,"total":100}`,
		`not json`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, body) }))
			defer server.Close()
			if err := PullModelWithProgress(context.Background(), server.URL, "test", nil); err == nil {
				t.Fatal("accepted failed stream")
			}
		})
	}
}
