package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oscar-investmatic/modeluplink-client/pkg/client"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
)

func TestPaidConnectionLimitHonorsAllowance(t *testing.T) {
	one, two, unlimited := 1, 2, 0
	for _, tc := range []struct {
		name                                 string
		limit                                *int
		paid, trialEndpoint, online, blocked bool
	}{
		{"legacy pilot offline", nil, true, false, false, true},
		{"online", &one, true, false, true, true},
		{"room for another", &two, true, false, false, false},
		{"unlimited", &unlimited, true, false, false, false},
		{"trial upgrade", &one, true, true, false, false},
		{"trial keeps its transfer flow", &one, false, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := client.Account{MaxEndpoints: tc.limit}
			if tc.paid {
				a.BillingState = "active"
			}
			e := client.Endpoint{ID: "ep_existing", Trial: tc.trialEndpoint, Online: tc.online}
			if got := paidConnectionLimit(a, []client.Endpoint{e}); (got != nil) != tc.blocked {
				t.Fatalf("blocked = %v, want %v", got != nil, tc.blocked)
			}
		})
	}
}

func TestDesktopReportsPaidLimitAndRejectsSetupBeforeLocalWork(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	existing := client.Endpoint{ID: "ep_existing", Slug: "existing", URL: "https://existing.example/v1", Active: true}
	remote := []client.Endpoint{existing}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation: %s", r.Method)
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case "/v1/me":
			json.NewEncoder(w).Encode(map[string]any{"billing_state": "active", "max_endpoints": 1})
		case "/v1/endpoints":
			json.NewEncoder(w).Encode(map[string]any{"data": remote})
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	t.Setenv("MODELUPLINK_CONTROL_URL", srv.URL)
	cfg, _ := localconfig.Load()
	cfg.AccountToken = "test-session"
	if err := localconfig.Save(cfg); err != nil {
		t.Fatal(err)
	}
	state, err := desktopDispatch(desktopRequest{Action: "state"})
	if err != nil || state.ConnectionLimit == nil || state.ConnectionLimit.ID != existing.ID || state.OtherTrial != nil || len(state.Endpoints) != 0 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	// The invalid source would fail differently if local inspection ran first.
	_, err = desktopDispatch(desktopRequest{Action: "connect", Model: "test-model", LocalURL: "not-a-url"})
	if err == nil || desktopError(err) != connectionLimitMessage {
		t.Fatalf("error=%v", err)
	}
	remote = nil
	state, err = desktopDispatch(desktopRequest{Action: "state"})
	if err != nil || state.ConnectionLimit != nil {
		t.Fatalf("stale limit: %+v %v", state, err)
	}
}
