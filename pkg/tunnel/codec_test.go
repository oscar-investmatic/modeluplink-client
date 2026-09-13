package tunnel

import (
	"net/http"
	"testing"
)

func TestRequestHeadersStripOriginAndReferer(t *testing.T) {
	in := http.Header{}
	in.Set("Origin", "https://editor.example")
	in.Set("Referer", "https://editor.example/page")
	in.Set("Authorization", "Bearer secret")
	in.Set("Content-Type", "application/json")
	in.Set("Accept", "text/event-stream")
	out := http.Header{}
	ApplyHeaders(out, RequestHeaders(in))
	for _, name := range []string{"Origin", "Referer", "Authorization"} {
		if out.Get(name) != "" {
			t.Fatalf("%s forwarded upstream: %q", name, out.Get(name))
		}
	}
	if out.Get("Content-Type") != "application/json" || out.Get("Accept") != "text/event-stream" {
		t.Fatalf("benign headers lost: %v", out)
	}
	resp := http.Header{}
	ApplyHeaders(resp, ResponseHeaders(http.Header{"Access-Control-Allow-Origin": {"*"}}))
	if resp.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("response headers must not be filtered by the request-only set")
	}
}
