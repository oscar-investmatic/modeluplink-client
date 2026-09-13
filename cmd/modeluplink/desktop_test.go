package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/pkg/client"
)

func TestDesktopExplainsEndpointConflictsWithoutExposingServerDetails(t *testing.T) {
	for _, tc := range []struct {
		code string
		want string
	}{
		{"trial_key_limit_reached", "Your trial already has an API key. Use your saved copy, or manage your keys in the dashboard to replace it."},
		{"endpoint_limit_reached", "Your account has reached its connection limit. Offline connections still count. Open your dashboard to manage your existing connections."},
		{"endpoint_name_taken", "This address is already taken. Choose another name and try again."},
	} {
		t.Run(tc.code, func(t *testing.T) {
			err := &client.APIError{Status: http.StatusConflict, Code: tc.code, Message: "private server details"}
			if got := desktopError(err); got != tc.want {
				t.Fatalf("desktopError = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDesktopCodeLoginStoresSessionAndFiltersRemoteMachines(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/web/code/start":
			if r.Header.Get("Authorization") != "" {
				t.Error("start carried authorization")
			}
			json.NewEncoder(w).Encode(map[string]string{"challenge_id": "wcode_test"})
		case "/v1/auth/web/code/verify":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["challenge_id"] != "wcode_test" || body["code"] != "012345" {
				t.Error("wrong code request")
			}
			json.NewEncoder(w).Encode(map[string]string{"session_token": "private-test-session"})
		case "/v1/me":
			if r.Header.Get("Authorization") != "Bearer private-test-session" {
				t.Error("missing saved session")
			}
			json.NewEncoder(w).Encode(map[string]any{"email": "native@example.test", "trial_status": "available"})
		case "/v1/endpoints":
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"id": "ep_here", "slug": "this-mac"}, {"id": "ep_elsewhere", "slug": "other-mac", "trial": true},
			}})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("MODELUPLINK_CONTROL_URL", server.URL)
	cfg, _ := localconfig.Load()
	cfg.Endpoints["this-mac"] = localconfig.Endpoint{ID: "ep_here", Slug: "this-mac"}
	if err := localconfig.Save(cfg); err != nil {
		t.Fatal(err)
	}
	start, err := desktopDispatch(desktopRequest{Action: "start_code", Email: "native@example.test"})
	if err != nil || start.Challenge != "wcode_test" {
		t.Fatalf("start: %+v %v", start, err)
	}
	reply, err := desktopDispatch(desktopRequest{Action: "verify_code", Challenge: start.Challenge, Code: "012345"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Account == nil || len(reply.Endpoints) != 1 || reply.Endpoints[0].ID != "ep_here" {
		t.Fatalf("state: %+v", reply)
	}
	if reply.OtherTrial == nil || reply.OtherTrial.ID != "ep_elsewhere" {
		t.Fatal("sign-in hid the trial on another computer")
	}
	encoded, _ := json.Marshal(reply)
	if strings.Contains(string(encoded), "private-test-session") {
		t.Fatal("session leaked into GUI response")
	}
	saved, _ := localconfig.Load()
	if saved.AccountToken != "private-test-session" || saved.ControlURL != server.URL {
		t.Fatal("sign-in not saved")
	}
	path, _ := localconfig.Path()
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("session config permissions")
	}
}

func TestDesktopRejectsForeignEndpointAndExpiredTrialBeforeSetup(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/me" {
			t.Fatalf("unexpected side effect: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"billing_state": "inactive", "trial_status": "expired"})
	}))
	defer server.Close()
	t.Setenv("MODELUPLINK_CONTROL_URL", server.URL)
	cfg, _ := localconfig.Load()
	cfg.AccountToken = "test-session"
	localconfig.Save(cfg)
	for _, action := range []string{"pause", "resume", "delete", "new_key"} {
		if _, err := desktopDispatch(desktopRequest{Action: action, Slug: "other-mac"}); err == nil {
			t.Fatalf("accepted %s for a foreign endpoint", action)
		}
	}
	if calls != 0 {
		t.Fatal("foreign endpoint caused an API request")
	}
	if _, err := desktopDispatch(desktopRequest{Action: "connect", Model: "llama3.2:3b"}); err == nil || !strings.Contains(err.Error(), "trial has ended") {
		t.Fatalf("connect: %v", err)
	}
	if calls != 1 {
		t.Fatal("expired trial started provisioning or checkout")
	}
}

func TestDesktopPaidStateOffersStableMissionNames(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/me":
			w.Write([]byte(`{"id":"acct_mission","billing_state":"active","trial_status":"not_applicable"}`))
		case "/v1/endpoints":
			w.Write([]byte(`{"data":[]}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("MODELUPLINK_CONTROL_URL", server.URL)
	cfg, _ := localconfig.Load()
	cfg.AccountToken = "session"
	if err := localconfig.Save(cfg); err != nil {
		t.Fatal(err)
	}
	first, err := desktopDispatch(desktopRequest{Action: "state"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := desktopDispatch(desktopRequest{Action: "state"})
	if err != nil || len(first.NameSuggestions) != 3 || strings.Join(first.NameSuggestions, ",") != strings.Join(second.NameSuggestions, ",") {
		t.Fatalf("mission suggestions are missing or unstable: %v / %v (%v)", first.NameSuggestions, second.NameSuggestions, err)
	}
}

func TestDesktopRejectsInvalidPaidMissionNameBeforeSetup(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/me" {
			t.Errorf("invalid name reached %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"acct_mission","billing_state":"active","trial_status":"not_applicable"}`))
	}))
	defer server.Close()
	t.Setenv("MODELUPLINK_CONTROL_URL", server.URL)
	cfg, _ := localconfig.Load()
	cfg.AccountToken = "session"
	if err := localconfig.Save(cfg); err != nil {
		t.Fatal(err)
	}
	_, err := desktopDispatch(desktopRequest{Action: "connect", Model: "llama3.2:3b", EndpointName: "x"})
	if err == nil || !strings.Contains(err.Error(), "endpoint name") || calls != 1 {
		t.Fatalf("invalid mission name was not rejected early: calls=%d err=%v", calls, err)
	}
}

func TestNativeProvisioningReturnsKeyWithoutPrintingIt(t *testing.T) {
	for _, ready := range []bool{true, false} {
		t.Run(map[bool]string{true: "ready", false: "pending"}[ready], func(t *testing.T) {
			f := newCLIFixture(t)
			f.control.me, f.control.created = meTrialAvailable, createdTrial
			if !ready {
				waitForReady = func(context.Context, string, string, *http.Client) error { return errors.New("still connecting") }
			}
			var got localconfig.Endpoint
			var key string
			var confirmed bool
			output, err := captureStdout(t, func() error {
				return serveWithResult([]string{"vllm", "--url", f.upstream.URL}, func(e localconfig.Endpoint, k string, r bool) { got, key, confirmed = e, k, r })
			})
			if ready && err != nil {
				t.Fatal(err)
			}
			if !ready && err == nil {
				t.Fatal("pending readiness should remain an error to the CLI caller")
			}
			if got.ID != "ep_trial" || key != "mup_key_secret" || confirmed != ready {
				t.Fatalf("missing structured credentials: %+v ready=%v", got, confirmed)
			}
			if strings.Contains(output, "mup_key_secret") || strings.Contains(output, "curl ") {
				t.Fatal("native flow printed credentials")
			}
		})
	}
}

func TestNativeSignInSurvivesFollowupStatusOutage(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/web/code/verify" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"session_token":"saved-session","account":{"email":"native@example.test","billing_state":"inactive"}}`))
		} else {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	t.Setenv("MODELUPLINK_CONTROL_URL", server.URL)
	reply, err := desktopDispatch(desktopRequest{Action: "verify_code", Challenge: "wcode_test", Code: "012345"})
	if err != nil || reply.Account == nil || reply.Account.Email != "native@example.test" || reply.Notice == "" {
		t.Fatalf("lost completed sign-in: %+v %v", reply, err)
	}
	cfg, _ := localconfig.Load()
	if cfg.AccountToken != "saved-session" {
		t.Fatal("completed session not preserved")
	}
}

func TestSetupErrorsIdentifyFailedStageWithoutLeakingDetails(t *testing.T) {
	for _, stage := range []string{"prepare", "download", "reserve", "start", "verify"} {
		message := desktopError(setupError(stage, errors.New("private-token /Users/private/file")))
		if strings.Contains(message, "private") || strings.Contains(message, "We couldn’t finish setting up") {
			t.Fatalf("unsafe or vague error: %s", message)
		}
	}
	message := desktopError(setupError("download", errors.New("write private-file: no space left on device")))
	if !strings.Contains(message, "ran out of space") {
		t.Fatal(message)
	}
}

func TestSetupUnavailableRegionKeepsDownloadedModelExplanation(t *testing.T) {
	message := desktopError(setupError("reserve", &client.APIError{Status: 422, Code: "invalid_region"}))
	if !strings.Contains(message, "model is ready on this Mac") || !strings.Contains(message, "no available region") {
		t.Fatal(message)
	}
}

func TestOtherComputerTrialIsReportedAndNeverImplicitlyMoved(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/me":
			w.Write([]byte(`{"trial_status":"active","billing_state":"inactive"}`))
		case "/v1/endpoints":
			w.Write([]byte(`{"data":[{"id":"ep_other","slug":"try-other","trial":true,"online":true}]}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("MODELUPLINK_CONTROL_URL", server.URL)
	cfg, _ := localconfig.Load()
	cfg.AccountToken = "session"
	localconfig.Save(cfg)
	state, err := desktopDispatch(desktopRequest{Action: "state"})
	if err != nil || state.OtherTrial == nil || state.OtherTrial.ID != "ep_other" || len(state.Endpoints) != 0 {
		t.Fatalf("state: %+v %v", state, err)
	}
	for _, request := range []desktopRequest{
		{Action: "connect", Model: "llama3.2:3b"},
		{Action: "move_trial", Model: "llama3.2:3b"},
		{Action: "move_trial", Model: "llama3.2:3b", MoveTrialID: "stale"},
	} {
		if _, err = desktopDispatch(request); err == nil {
			t.Fatal("setup started without explicit current-endpoint consent")
		}
	}
	if mutations != 0 {
		t.Fatal("discovery mutated account")
	}
}
