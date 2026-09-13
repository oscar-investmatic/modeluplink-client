package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestOtherComputerChoiceRequiresExplicitConfirmation(t *testing.T) {
	u, h := newTestUI(t)
	other := &endpoint{ID: "ep_mac", Slug: "try-mac", URL: "https://try-mac.example/v1", Online: true}
	u.managedSetup = true
	u.apply(&helperReply{Account: &account{TrialStatus: "active", TrialRemaining: 100}, OtherTrial: other, Models: []string{"llama3.2:3b"}})
	u.render()
	if !strings.Contains(labels(u), "Your trial is already sharing a model from another computer.") {
		t.Fatal(labels(u))
	}
	test.Tap(button(t, u, "Keep using that computer"))
	if !u.keepingOtherComputer || u.preparingMove {
		t.Fatal("keep did not remain on other computer")
	}
	test.Tap(button(t, u, "Move sharing to this computer"))
	if !u.preparingMove {
		t.Fatal("move selection not shown")
	}
	var confirm func()
	u.confirm = func(title, message, action string, onYes func()) {
		if !strings.Contains(message, "new address and API key") || !strings.Contains(message, "expiry stay the same") {
			t.Fatal(message)
		}
		confirm = onYes
	}
	test.Tap(button(t, u, "Move sharing to this computer"))
	if confirm == nil {
		t.Fatal("no confirmation")
	}
	select {
	case r := <-h.calls:
		t.Fatalf("mutated before confirmation: %v", r)
	default:
	}
	h.reply = helperReply{Endpoint: &endpoint{ID: "ep_ubuntu", Slug: "try-ubuntu", Online: true}}
	confirm()
	request := awaitCall(t, h)
	if request["action"] != "move_trial" || request["move_trial_id"] != "ep_mac" {
		t.Fatal(request)
	}
	close(h.release)
	u.pending.Wait()
	if u.otherTrial != nil || u.preparingMove || len(u.endpoints) != 1 {
		t.Fatal("move result not applied")
	}
}

func TestNewRemoteConnectionInvalidatesOldMoveChoice(t *testing.T) {
	u, _ := newTestUI(t)
	u.otherTrial = &endpoint{ID: "ep_old"}
	u.preparingMove = true
	u.keepingOtherComputer = true
	u.apply(&helperReply{OtherTrial: &endpoint{ID: "ep_new"}})
	if u.preparingMove || u.keepingOtherComputer {
		t.Fatal("stale transfer choice carried into another endpoint")
	}
}

func TestMovingAwayClearsOldResultScreen(t *testing.T) {
	u, _ := newTestUI(t)
	u.result = &connectResult{endpoint: endpoint{ID: "ep_old", Slug: "try-old"}}
	u.apply(&helperReply{Account: &account{TrialStatus: "active"}, OtherTrial: &endpoint{ID: "ep_new", Slug: "try-new"}})
	u.render()
	if u.result != nil || !strings.Contains(labels(u), "another computer") {
		t.Fatal("stale success screen hid the transfer")
	}
}

func TestPaidConnectionLimitShownBeforeSetupAndClearsOnRefresh(t *testing.T) {
	u, h := newTestUI(t)
	account := &account{BillingState: "active"}
	initial := u.stateSignature()
	u.apply(&helperReply{Account: account, ConnectionLimit: &endpoint{ID: "ep_mac", URL: "https://existing.example/v1"}})
	u.render()
	for _, text := range []string{"You already have a connection", "offline", "https://existing.example/v1"} {
		if !strings.Contains(labels(u), text) {
			t.Fatalf("missing %q: %s", text, labels(u))
		}
	}
	if strings.Contains(labels(u), "Connect a model server") {
		t.Fatal("setup shown despite limit")
	}
	button(t, u, "Open dashboard")
	button(t, u, "Refresh connections")
	select {
	case call := <-h.calls:
		t.Fatalf("warning triggered helper action: %v", call)
	default:
	}
	blocked := u.stateSignature()
	if initial == blocked {
		t.Fatal("warning omitted from refresh state")
	}
	u.apply(&helperReply{Account: account})
	u.render()
	if blocked == u.stateSignature() || !strings.Contains(labels(u), "Connect a model server") {
		t.Fatal("cleared limit did not restore setup")
	}
}
