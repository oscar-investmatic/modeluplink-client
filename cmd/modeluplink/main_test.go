package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
)

func TestCanonicalOllamaModel(t *testing.T) {
	for input, want := range map[string]string{
		"llama3.2":                  "llama3.2:latest",
		"llama3.2:3b":               "llama3.2:3b",
		"team/model":                "team/model:latest",
		"localhost:5000/team/model": "localhost:5000/team/model:latest",
	} {
		if got := canonicalOllamaModel(input); got != want {
			t.Errorf("canonicalOllamaModel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWaitForEndpointVerifiesPublicAuthenticatedModelsRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer mup_test_secret" {
			t.Fatalf("authorization = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForEndpoint(ctx, server.URL+"/v1", "mup_test_secret", server.Client()); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForEndpointRetriesTemporaryFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "not connected", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := waitForEndpoint(ctx, server.URL+"/v1", "mup_test_secret", server.Client()); err != nil {
		t.Fatal(err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d", got)
	}
}

func TestWaitForEndpointFailsImmediatelyOnRejectedKey(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForEndpoint(ctx, server.URL+"/v1", "bad-key", server.Client()); err == nil {
		t.Fatal("expected rejected API key to fail readiness")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d", got)
	}
}

// fakeControlPlane serves the account, endpoint and billing routes the CLI
// uses, with the free tier fields from the contract.
type fakeControlPlane struct {
	mu            sync.Mutex
	me            string
	afterCheckout string
	created       string
	endpoints     string
	createBodies  []map[string]any
	checkouts     int
	server        *httptest.Server
}

func newFakeControlPlane(t *testing.T) *fakeControlPlane {
	f := &fakeControlPlane{endpoints: `{"data":[]}`}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/regions" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer account-token" {
			t.Errorf("account authorization = %q", r.Header.Get("Authorization"))
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/me":
			_, _ = io.WriteString(w, f.me)
		case "POST /v1/billing/checkout":
			f.checkouts++
			if f.afterCheckout != "" {
				f.me = f.afterCheckout
			}
			_, _ = io.WriteString(w, `{"url":"https://checkout.stripe.test/session"}`)
		case "POST /v1/endpoints":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.createBodies = append(f.createBodies, body)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, f.created)
		case "GET /v1/endpoints":
			_, _ = io.WriteString(w, f.endpoints)
		default:
			t.Errorf("unexpected control request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

const (
	meTrialAvailable = `{"id":"acc_1","email":"a@example.com","billing_state":"inactive","trial_requests_used":0,"trial_transfer_used":0,"trial_status":"available","trial_remaining":500,"trial_transfer_remaining":250000000,"trial_expires_at":null}`
	meTrialActive    = `{"id":"acc_1","email":"a@example.com","billing_state":"inactive","trial_started_at":"2026-09-04T10:00:00Z","trial_requests_used":3,"trial_transfer_used":1000000,"trial_status":"active","trial_remaining":497,"trial_transfer_remaining":249000000,"trial_expires_at":"2026-09-11T10:00:00Z"}`
	meTrialExhausted = `{"id":"acc_1","email":"a@example.com","billing_state":"inactive","trial_started_at":"2026-09-04T10:00:00Z","trial_requests_used":500,"trial_transfer_used":1000000,"trial_status":"exhausted","trial_remaining":0,"trial_transfer_remaining":0,"trial_expires_at":"2026-09-11T10:00:00Z"}`
	mePaid           = `{"id":"acc_1","email":"a@example.com","billing_state":"active","trial_requests_used":3,"trial_transfer_used":1000000,"trial_status":"not_applicable","trial_remaining":0,"trial_transfer_remaining":0,"trial_expires_at":null}`
	createdTrial     = `{"endpoint":{"id":"ep_trial","slug":"try-abcd2345","display_name":"try-abcd2345","engine":"vllm","region":"eu","url":"https://try-abcd2345.modeluplink.test/v1","online":false,"active":true,"cors_origins":[],"trial":true},"agent_token":"mua_agent","api_key":"mup_key_secret","relay_url":"wss://relay.test/tunnel","trial_remaining":500,"trial_transfer_remaining":250000000,"trial_expires_at":null,"requested_name_ignored":true}`
	createdPaid      = `{"endpoint":{"id":"ep_paid","slug":"mybox","display_name":"mybox","engine":"vllm","region":"eu","url":"https://mybox.modeluplink.test/v1","online":false,"active":true,"cors_origins":[],"trial":false},"agent_token":"mua_agent","api_key":"mup_key_secret","relay_url":"wss://relay.test/tunnel","replaced_trial_endpoint_ids":["ep_trial"]}`
)

// cliFixture isolates the local config directory and replaces the process
// seams (browser, background service, readiness probe) for one test.
type cliFixture struct {
	control      *fakeControlPlane
	upstream     *httptest.Server
	opened       []string
	installed    []string
	uninstalled  []string
	readyChecked bool
}

func newCLIFixture(t *testing.T) *cliFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("MODELUPLINK_CONFIG_DIR", filepath.Join(root, "config", "modeluplink"))
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("MODELUPLINK_CONTROL_URL", "")
	t.Setenv("MODELUPLINK_ACCOUNT_TOKEN", "")
	f := &cliFixture{control: newFakeControlPlane(t)}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"model-a"}]}`)
	}))
	t.Cleanup(f.upstream.Close)
	if err := localconfig.Save(localconfig.Config{ControlURL: f.control.server.URL, AccountToken: "account-token", Endpoints: map[string]localconfig.Endpoint{}}); err != nil {
		t.Fatal(err)
	}
	prevBrowser, prevInstall, prevUninstall, prevReady, prevPoll := openBrowser, installService, uninstallService, waitForReady, checkoutPollInterval
	openBrowser = func(target string) error { f.opened = append(f.opened, target); return nil }
	installService = func(_, _, slug string) (string, error) {
		f.installed = append(f.installed, slug)
		return "/tmp/fake.log", nil
	}
	uninstallService = func(slug string) error { f.uninstalled = append(f.uninstalled, slug); return nil }
	waitForReady = func(context.Context, string, string, *http.Client) error { f.readyChecked = true; return nil }
	checkoutPollInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		openBrowser, installService, uninstallService, waitForReady, checkoutPollInterval = prevBrowser, prevInstall, prevUninstall, prevReady, prevPoll
	})
	return f
}

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = writer
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&buf, reader); close(done) }()
	runErr := fn()
	os.Stdout = prev
	_ = writer.Close()
	<-done
	_ = reader.Close()
	return buf.String(), runErr
}

func TestServeTrialIgnoresNameAndPrintsTrialLine(t *testing.T) {
	f := newCLIFixture(t)
	f.control.me, f.control.created = meTrialAvailable, createdTrial
	out, err := captureStdout(t, func() error { return run([]string{"serve", "vllm", "--url", f.upstream.URL, "--name", "mybox"}) })
	if err != nil {
		t.Fatalf("serve: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Free trial uses a temporary address; you choose the permanent name when you subscribe.",
		"Free trial: 500 requests and 250 MB transfer left; starts on the first successful API call. Upgrade: modeluplink upgrade",
		"Base URL: https://try-abcd2345.modeluplink.test/v1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(f.control.createBodies) != 1 || f.control.createBodies[0]["name"] != "" {
		t.Fatalf("create bodies = %+v", f.control.createBodies)
	}
	if f.control.checkouts != 0 || len(f.opened) != 0 {
		t.Fatalf("trial mode opened checkout: %d %v", f.control.checkouts, f.opened)
	}
	if len(f.installed) != 1 || f.installed[0] != "try-abcd2345" {
		t.Fatalf("installed services = %v", f.installed)
	}
	cfg, err := localconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if local := cfg.Endpoints["try-abcd2345"]; local.ID != "ep_trial" || !local.Trial {
		t.Fatalf("local trial endpoint = %+v", local)
	}
}

func TestServeExhaustedTrialOpensCheckoutThenReplacesTrialEndpoint(t *testing.T) {
	f := newCLIFixture(t)
	f.control.me, f.control.afterCheckout, f.control.created = meTrialExhausted, mePaid, createdPaid
	cfg, _ := localconfig.Load()
	cfg.Endpoints["try-abcd2345"] = localconfig.Endpoint{ID: "ep_trial", Slug: "try-abcd2345", Engine: "vllm", Trial: true}
	if err := localconfig.Save(cfg); err != nil {
		t.Fatal(err)
	}
	trialPath, err := localconfig.SaveEndpoint(cfg.Endpoints["try-abcd2345"])
	if err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return run([]string{"serve", "vllm", "--url", f.upstream.URL, "--name", "mybox"}) })
	if err != nil {
		t.Fatalf("serve: %v\n%s", err, out)
	}
	for _, want := range []string{"Your free trial is over.", "https://checkout.stripe.test/session", "Subscription active.", "Replaced trial endpoint try-abcd2345", "Base URL: https://mybox.modeluplink.test/v1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Free trial:") {
		t.Fatalf("paid endpoint printed a trial line:\n%s", out)
	}
	if len(f.opened) != 1 || f.opened[0] != "https://checkout.stripe.test/session" {
		t.Fatalf("browser opened = %v", f.opened)
	}
	if f.control.createBodies[0]["name"] != "mybox" {
		t.Fatalf("paid create body = %+v", f.control.createBodies[0])
	}
	if len(f.uninstalled) != 1 || f.uninstalled[0] != "try-abcd2345" {
		t.Fatalf("uninstalled services = %v", f.uninstalled)
	}
	if _, statErr := os.Stat(trialPath); !os.IsNotExist(statErr) {
		t.Fatalf("trial endpoint file remains: %v", statErr)
	}
	cfg, _ = localconfig.Load()
	if _, still := cfg.Endpoints["try-abcd2345"]; still {
		t.Fatal("replaced trial endpoint remains in local config")
	}
	if local := cfg.Endpoints["mybox"]; local.ID != "ep_paid" || local.Trial {
		t.Fatalf("local paid endpoint = %+v", local)
	}
}

func TestUpgradePrintsSuccessAfterCheckout(t *testing.T) {
	f := newCLIFixture(t)
	f.control.me, f.control.afterCheckout = meTrialActive, mePaid
	out, err := captureStdout(t, func() error { return run([]string{"upgrade"}) })
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if !strings.Contains(out, "https://checkout.stripe.test/session") || len(f.opened) != 1 {
		t.Fatalf("checkout not opened:\n%s", out)
	}
	if !strings.Contains(out, "Subscription active. Run: modeluplink serve ollama --name NAME to reserve your permanent address (your trial endpoint will be replaced).") {
		t.Fatalf("success message missing:\n%s", out)
	}
}

func TestStatusPrintsTrialLineAndMarksTrialEndpoints(t *testing.T) {
	f := newCLIFixture(t)
	f.control.me = meTrialActive
	f.control.endpoints = `{"data":[{"id":"ep_trial","slug":"try-abcd2345","engine":"vllm","region":"eu","url":"https://try-abcd2345.modeluplink.test/v1","online":true,"active":true,"cors_origins":[],"trial":true}]}`
	out, err := captureStdout(t, func() error { return run([]string{"status"}) })
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	wantLine := "Free trial: 497 requests and 249 MB transfer left, ends " + time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC).Local().Format("Jan 2, 2006")
	if !strings.Contains(out, wantLine) {
		t.Fatalf("output lacks %q:\n%s", wantLine, out)
	}
	if !strings.Contains(out, "try-abcd2345             trial   online  https://try-abcd2345.modeluplink.test/v1") {
		t.Fatalf("trial endpoint row missing:\n%s", out)
	}
}

func TestDeleteCleansLocalStateAfterMissingOrExpiredRemote(t *testing.T) {
	for _, status := range []int{401, 404, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			f := newCLIFixture(t)
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					t.Error(r.Method)
				}
				w.WriteHeader(status)
			}))
			defer remote.Close()
			e := localconfig.Endpoint{ID: "ep_cleanup", Slug: "cleanup-gpu", AgentToken: "local-secret"}
			path, err := localconfig.SaveEndpoint(e)
			if err != nil {
				t.Fatal(err)
			}
			if err = localconfig.Save(localconfig.Config{ControlURL: remote.URL, AccountToken: "account-token", Endpoints: map[string]localconfig.Endpoint{e.Slug: e}}); err != nil {
				t.Fatal(err)
			}
			err = deleteEndpoint([]string{e.Slug, "--yes"})
			if status == 500 {
				if err == nil {
					t.Fatal("server error must be reported")
				}
				if _, err = os.Stat(path); err != nil {
					t.Fatal("unexpected cleanup on server error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("local secret file remained", err)
			}
			cfg, err := localconfig.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Endpoints) != 0 || len(f.uninstalled) != 1 {
				t.Fatal("local endpoint/service was not removed")
			}
		})
	}
}
