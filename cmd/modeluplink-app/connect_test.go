package main

import (
	"fyne.io/fyne/v2/test"
	"strings"
	"testing"
)

func TestExistingServerIsPrimaryAndNeedsReadiness(t *testing.T) {
	u, h := newTestUI(t)
	u.account = &account{BillingState: "inactive", TrialStatus: "available"}
	u.render()
	button(t, u, "Find local servers")
	button(t, u, "Find models")
	button(t, u, "Help me set up a model with Ollama")
	if !button(t, u, "Start sharing").Disabled() {
		t.Fatal("untested server can start sharing")
	}
	if len(h.calls) != 0 {
		t.Fatal("render scanned the computer")
	}
	test.Tap(button(t, u, "Help me set up a model with Ollama"))
	button(t, u, "Connect my model")
}
func TestAttachedConnectionPassesCredentialOnlyToHelper(t *testing.T) {
	u, h := newTestUI(t)
	u.account = &account{BillingState: "active"}
	u.localURL = "http://127.0.0.1:1234/v1"
	u.localKey = "private-upstream"
	u.localModel = "friends-model"
	u.testedSource = u.sourceFingerprint()
	h.reply = helperReply{Endpoint: &endpoint{ID: "ep_test", Slug: "shared-gpu", Online: true}, LocalSources: map[string]localServer{"shared-gpu": {Ownership: "external"}}}
	u.connect()
	r := awaitCall(t, h)
	if r["local_url"] != u.localURL || r["local_key"] != "private-upstream" || r["model"] != "friends-model" {
		t.Fatal("wrong local connection request")
	}
	close(h.release)
	u.pending.Wait()
	if u.localKey != "" {
		t.Fatal("setup retained credential after saving")
	}
	u.paused["shared-gpu"] = true
	u.render()
	if strings.Contains(labels(u), "model memory released") {
		t.Fatal("attached runtime reported an unload")
	}
}
