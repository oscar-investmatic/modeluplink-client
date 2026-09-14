package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type testKeys struct{}

func (testKeys) save(string, string) error   { return nil }
func (testKeys) read(string) (string, error) { return "", errKeyNotFound }
func (testKeys) remove(string)               {}

type blockingHelper struct {
	calls   chan map[string]string
	release chan struct{}
	reply   helperReply
}

func (h *blockingHelper) run(ctx context.Context, r map[string]string, p func(progressEvent)) (helperReply, error) {
	h.calls <- r
	select {
	case <-h.release:
		return h.reply, nil
	case <-ctx.Done():
		return helperReply{}, ctx.Err()
	}
}
func newTestUI(t *testing.T) (*ui, *blockingHelper) {
	t.Helper()
	a := test.NewApp()
	a.Settings().SetTheme(uplinkTheme{theme.DefaultTheme()})
	w := a.NewWindow("Model Uplink")
	h := &blockingHelper{calls: make(chan map[string]string, 8), release: make(chan struct{})}
	u := newUI(a, w, h, testKeys{})
	u.loading = false
	w.SetContent(u.content)
	w.Resize(fyne.NewSize(520, 680))
	t.Cleanup(func() { u.stop(); u.pending.Wait(); w.Close(); a.Quit() })
	return u, h
}
func awaitCall(t *testing.T, h *blockingHelper) map[string]string {
	t.Helper()
	select {
	case r := <-h.calls:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("helper not called")
		return nil
	}
}
func button(t *testing.T, u *ui, text string) *widget.Button {
	t.Helper()
	for _, o := range test.LaidOutObjects(u.content) {
		if b, ok := o.(*widget.Button); ok && b.Text == text {
			return b
		}
	}
	t.Fatalf("missing button %q", text)
	return nil
}
func labels(u *ui) string {
	var texts []string
	for _, o := range test.LaidOutObjects(u.content) {
		if l, ok := o.(*widget.Label); ok {
			texts = append(texts, l.Text)
		}
	}
	return strings.Join(texts, "\n")
}
func TestInstalledUpdateTellsUserToCloseWindow(t *testing.T) {
	u, _ := newTestUI(t)
	u.render()
	if strings.Contains(labels(u), updateInstalledNotice) {
		t.Fatal("update notice shown before an update was installed")
	}
	u.updateInstalled = true
	u.render()
	if !strings.Contains(labels(u), "Close this window, then open Model Uplink again") {
		t.Fatalf("update notice missing:\n%s", labels(u))
	}
}
func TestPastingCodeSubmitsOnceWithoutClick(t *testing.T) {
	u, h := newTestUI(t)
	u.challenge = "challenge"
	u.email = "test@example.invalid"
	u.render()
	var entry *widget.Entry
	for _, o := range test.LaidOutObjects(u.content) {
		if e, ok := o.(*widget.Entry); ok && e.PlaceHolder == "000000" {
			entry = e
			break
		}
	}
	if entry == nil {
		t.Fatal("code entry missing")
	}
	entry.SetText("12 34-56")
	r := awaitCall(t, h)
	if r["action"] != "verify_code" || r["code"] != "123456" {
		t.Fatal("wrong OTP request")
	}
	u.verify() // A second submit while the request is in flight is ignored.
	if len(h.calls) != 0 {
		t.Fatal("duplicate code redemption")
	}
	close(h.release)
	u.pending.Wait()
}
func TestStopFailureRetainsRetryAndNeverClaimsMemoryReleased(t *testing.T) {
	u, h := newTestUI(t)
	e := endpoint{ID: "e", Slug: "test", URL: "https://example.invalid/v1", Online: true}
	u.account = &account{}
	u.endpoints = []endpoint{e}
	u.render()
	h.reply = helperReply{Stopped: map[string]bool{"test": true}, MemoryReleasePending: map[string]bool{"test": true}, Notice: "Ollama has not confirmed memory release"}
	test.Tap(button(t, u, "Stop sharing"))
	r := awaitCall(t, h)
	if r["action"] != "pause" {
		t.Fatal("wrong stop request")
	}
	close(h.release)
	u.pending.Wait()
	button(t, u, "Release model memory")
	button(t, u, "Start sharing")
	if strings.Contains(labels(u), "model memory released") || !strings.Contains(labels(u), h.reply.Notice) {
		t.Fatal("memory failure concealed")
	}
}
func TestCurrentHelperStateOverridesOldPausedPreferences(t *testing.T) {
	u, _ := newTestUI(t)
	u.paused["test"] = true
	u.apply(&helperReply{Account: &account{}, Endpoints: []endpoint{{Slug: "test", Online: true}}, Stopped: map[string]bool{"test": false}, SharedModels: map[string][]string{"test": {"llama3.2:3b"}}, Activity: map[string]modelActivity{"test": {Model: "llama3.2:3b", Running: 1, Waiting: 3}}})
	u.render()
	if !u.connected() || !strings.Contains(labels(u), "1 running · 3 waiting") {
		t.Fatal("authoritative state or activity missing")
	}
}

func TestTrialLimitShowsUpgradeAndKeepsStopAvailable(t *testing.T) {
	u, h := newTestUI(t)
	u.account = &account{BillingState: "inactive", TrialStatus: "exhausted"}
	u.endpoints = []endpoint{{ID: "e", Slug: "test", URL: "https://example.invalid/v1", Online: true}}
	u.render()
	button(t, u, "Subscribe to Agent")
	button(t, u, "Stop sharing")
	if u.connected() || !strings.Contains(labels(u), "TRIAL FINISHED") || strings.Contains(labels(u), "Your model stays online") {
		t.Fatal("exhausted trial claims to be serving")
	}
	u.paused["test"] = true
	u.render()
	button(t, u, "Subscribe to resume sharing")
	// An entitlement refresh must restore paid UI without overriding an
	// explicit Stop, changing the URL, or issuing a resume command.
	u.apply(&helperReply{Account: &account{BillingState: "active", TrialStatus: "not_applicable"}, Endpoints: u.endpoints, Stopped: map[string]bool{"test": true}})
	u.render()
	button(t, u, "Manage billing")
	button(t, u, "Start sharing")
	if u.connected() || !u.paused["test"] || u.endpoints[0].URL != "https://example.invalid/v1" || len(h.calls) != 0 {
		t.Fatal("payment changed an existing stopped connection")
	}
}

func TestFreshAccountCanChoosePaidBeforeConnecting(t *testing.T) {
	u, h := newTestUI(t)
	u.managedSetup = true
	u.account = &account{BillingState: "inactive", TrialStatus: "available"}
	u.render()
	button(t, u, "Subscribe to Agent")
	button(t, u, "Connect my model")
	if len(h.calls) != 0 {
		t.Fatal("showing paid choice started model setup")
	}
}
func TestSharingUsesCurrentContractAndPreservesAccount(t *testing.T) {
	u, h := newTestUI(t)
	u.account = &account{Email: "test@example.invalid"}
	h.reply = helperReply{SharedModels: map[string][]string{"test": {"team/model, Q4", " spaced ID "}}}
	u.updateSharing(endpoint{Slug: "test"}, []string{"team/model, Q4", " spaced ID "})
	r := awaitCall(t, h)
	if r["action"] != "share_models" || r["model_ids"] != `["team/model, Q4"," spaced ID "]` {
		t.Fatal("wrong sharing request")
	}
	close(h.release)
	u.pending.Wait()
	if u.account == nil || len(u.sharedModels["test"]) != 2 {
		t.Fatal("partial reply destroyed account state")
	}
}

func TestPaidSetupSendsChosenMissionName(t *testing.T) {
	u, h := newTestUI(t)
	u.managedSetup = true
	u.apply(&helperReply{
		Account:         &account{BillingState: "active"},
		Models:          []string{"llama3.2:3b"},
		NameSuggestions: []string{"quiet-rover", "silver-comet", "bold-atlas"},
	})
	u.endpointName = "silver-comet"
	u.render()
	if !strings.Contains(labels(u), "ENDPOINT NAME") {
		t.Fatal("paid mission-name controls are missing")
	}
	test.Tap(button(t, u, "Connect my model"))
	request := awaitCall(t, h)
	if request["endpoint_name"] != "silver-comet" {
		t.Fatalf("mission name missing from helper request: %v", request)
	}
	close(h.release)
	u.pending.Wait()
}
func TestStaleRefreshCannotOverwriteStop(t *testing.T) {
	u, h := newTestUI(t)
	u.account = &account{}
	u.endpoints = []endpoint{{Slug: "test", Online: true}}
	h.reply = helperReply{Account: &account{}, Stopped: map[string]bool{"test": false}}
	u.refresh(true)
	awaitCall(t, h)
	u.begin("Stopping…")
	u.paused["test"] = true
	u.busy = false
	close(h.release)
	u.pending.Wait()
	if !u.paused["test"] {
		t.Fatal("old refresh overwrote stop state")
	}
}

func TestMissingActivityClearsStaleServingStatus(t *testing.T) {
	u, _ := newTestUI(t)
	u.activity["test"] = modelActivity{Running: 1, Waiting: 3}
	u.apply(&helperReply{Account: &account{}, Endpoints: []endpoint{{Slug: "test", Online: true}}})
	if len(u.activity) != 0 {
		t.Fatal("stale request counters retained")
	}
}

func TestSharingPickerDefaultsToOneModel(t *testing.T) {
	u, _ := newTestUI(t)
	u.account = &account{}
	u.models = []string{"one:latest", "two:latest"}
	u.sharedModels["test"] = []string{"one:latest"}
	u.editSharing(endpoint{Slug: "test"})
	overlay := u.window.Canvas().Overlays().Top()
	var choices *widget.CheckGroup
	var multiple *widget.Check
	for _, o := range test.LaidOutObjects(overlay) {
		if c, ok := o.(*widget.CheckGroup); ok {
			choices = c
		}
		if c, ok := o.(*widget.Check); ok && c.Text == "Share more than one model" {
			multiple = c
		}
	}
	if choices == nil || multiple == nil {
		t.Fatal("sharing editor controls missing")
	}
	choices.SetSelected([]string{"one:latest", "two:latest"})
	if len(choices.Selected) != 1 || choices.Selected[0] != "two:latest" {
		t.Fatal("single-model mode shared two models")
	}
	multiple.SetChecked(true)
	choices.SetSelected([]string{"one:latest", "two:latest"})
	if len(choices.Selected) != 2 {
		t.Fatal("multiple-model opt-in failed")
	}
}

func TestOlderServerDoesNotInventZeroTransferAllowance(t *testing.T) {
	u, _ := newTestUI(t)
	u.account = &account{TrialStatus: "active", TrialRemaining: 20}
	if strings.Contains(u.allowance(), "transfer") {
		t.Fatal("invented transfer allowance from missing server field")
	}
	zero := int64(0)
	u.account.TrialTransferRemaining = &zero
	if !strings.Contains(u.allowance(), "0 B transfer left") {
		t.Fatal("real exhausted transfer allowance omitted")
	}
}

func TestQuietRefreshRedrawsOnlyWhenStateChanges(t *testing.T) {
	u, h := newTestUI(t)
	u.account = &account{Email: "person@example.com"}
	u.endpoints = []endpoint{{ID: "ep_1", Slug: "test", URL: "https://test.example/v1", Online: true}}
	u.render()
	before := u.content.Objects[0]
	h.reply = helperReply{Account: &account{Email: "person@example.com"}, Endpoints: []endpoint{{ID: "ep_1", Slug: "test", URL: "https://test.example/v1", Online: true}}}
	u.refresh(true)
	awaitCall(t, h)
	close(h.release)
	u.pending.Wait()
	if u.content.Objects[0] != before {
		t.Fatal("unchanged state rebuilt the screen")
	}
	h.reply = helperReply{Account: &account{Email: "person@example.com"}, Endpoints: []endpoint{{ID: "ep_1", Slug: "test", URL: "https://test.example/v1", Online: false}}}
	u.refresh(true)
	awaitCall(t, h)
	u.pending.Wait()
	if u.content.Objects[0] == before {
		t.Fatal("changed state did not rebuild the screen")
	}
}
