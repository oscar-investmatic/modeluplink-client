package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDesktopInspectUsesOnlyLocalAPIAndKeepsCredentialPrivate(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-local-key" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"friends-model"}]}`))
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected management route %s", r.URL.Path)
		}
		posts.Add(1)
		w.Write([]byte(`{"choices":[{"message":{"content":"ready"}}]}`))
	}))
	defer server.Close()
	r := desktopRequest{Action: "inspect_source", LocalURL: server.URL, LocalKey: "private-local-key"}
	out, err := desktopDispatch(r)
	if err != nil || out.Source.Status != "server_available" || out.Ready || posts.Load() != 0 {
		t.Fatalf("inspect %+v %v", out, err)
	}
	r.Action = "test_source"
	r.Model = "friends-model"
	out, err = desktopDispatch(r)
	if err != nil || !out.Ready || posts.Load() != 1 {
		t.Fatalf("test %+v %v", out, err)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), r.LocalKey) {
		t.Fatal("credential in helper reply")
	}
	r.Model = "not-selected"
	out, err = desktopDispatch(r)
	if err != nil || out.Source.Status != "model_unavailable" || posts.Load() != 1 {
		t.Fatal("unlisted model tested")
	}
	r.LocalKey = ""
	out, err = desktopDispatch(r)
	if err != nil || out.Source.Status != "authentication_required" {
		t.Fatal("missing authentication status")
	}
}

func TestInvalidSourceURLDoesNotEchoCredentials(t *testing.T) {
	out, err := desktopInspect(desktopRequest{Action: "inspect_source", LocalURL: "http://user:private-password@127.0.0.1:1234/v1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "private-password") || out.Source.Status != "invalid_url" {
		t.Fatal("invalid URL was echoed or accepted")
	}
}

func TestDesktopPreservesExactModelIDs(t *testing.T) {
	for _, request := range []desktopRequest{
		{Model: "team/model, Q4"},
		{ModelIDs: `["team/model, Q4"," spaced ID "]`},
	} {
		got, err := desktopModels(request)
		if err != nil || len(got) == 0 || got[0] != "team/model, Q4" {
			t.Fatalf("model IDs changed: %q %v", got, err)
		}
		if len(got) > 1 && got[1] != " spaced ID " {
			t.Fatalf("model ID trimmed: %q", got)
		}
	}
	for _, raw := range []string{`[]`, `null`, `"one"`, `[""]`} {
		if _, err := desktopModels(desktopRequest{ModelIDs: raw}); err == nil {
			t.Fatalf("accepted invalid selection %s", raw)
		}
	}
}
