package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAutoRegionUsesReachableFasterRelay(t *testing.T) {
	us := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Error("wrong probe path")
		}
		w.WriteHeader(200)
	}))
	defer us.Close()
	eu := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(50 * time.Millisecond); w.WriteHeader(200) }))
	defer eu.Close()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"eu": strings.Replace(eu.URL, "https:", "wss:", 1) + "/v1/relay/connect", "us": strings.Replace(us.URL, "https:", "wss:", 1) + "/v1/relay/connect"})
	}))
	defer control.Close()
	if got := chooseRegionWithClient(control.URL, us.Client()); got != "us" {
		t.Fatalf("selected %s, want us", got)
	}
	us.Close()
	if got := chooseRegionWithClient(control.URL, eu.Client()); got != "eu" {
		t.Fatalf("failed US probe should leave reachable EU, got %s", got)
	}
}
