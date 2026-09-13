package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/oscar-investmatic/modeluplink-client/internal/desktopcontract"
)

func TestSharedDesktopContract(t *testing.T) {
	for _, c := range desktopcontract.Cases() {
		t.Run(c.Name, func(t *testing.T) {
			var stages []string
			reply, err := parseHelperStream(singleByteReader{strings.NewReader(c.Wire)}, func(p progressEvent) { stages = append(stages, p.Stage) })
			if (err == nil) != c.Accept {
				t.Fatalf("accept=%v: %v", c.Accept, err)
			}
			if !c.Accept {
				return
			}
			if !slices.Equal(stages, c.Stages) {
				t.Fatalf("progress stages: %v", stages)
			}
			if err := desktopcontract.Check(reply, c.Expected); err != nil {
				t.Fatal(err)
			}
			u, _ := newTestUI(t)
			u.startupEnabled = c.Name == "startup_disabled"
			u.apply(&reply)
			switch c.Name {
			case "startup_disabled":
				if u.startupEnabled {
					t.Fatal("disabled startup ignored")
				}
			case "startup_enabled":
				if !u.startupEnabled {
					t.Fatal("enabled startup ignored")
				}
			case "trial_exhausted":
				if !u.trialFinished() || u.connected() {
					t.Fatal("exhausted trial shown online")
				}
			case "disconnected":
				if u.connected() {
					t.Fatal("offline endpoint shown online")
				}
			case "trial_transfer_required":
				if u.otherTrial == nil || u.preparingMove {
					t.Fatal("transfer must require an explicit action")
				}
			case "memory_release_pending":
				if !u.paused["fixture"] || !u.memoryReleasePending["fixture"] || u.connected() {
					t.Fatal("unload failure concealed")
				}
			}
		})
	}
}

type stopFailureHelper struct{ stop *blockingHelper }

func (h stopFailureHelper) run(ctx context.Context, request map[string]string, progress func(progressEvent)) (helperReply, error) {
	if request["action"] == "state" {
		return helperReply{Account: &account{}, Stopped: map[string]bool{"fixture": true}, MemoryReleasePending: map[string]bool{"fixture": true}}, nil
	}
	return h.stop.run(ctx, request, progress)
}

func TestStopAllPreservesFailureAfterRefresh(t *testing.T) {
	u, h := newTestUI(t)
	u.helper = stopFailureHelper{stop: h}
	h.reply = helperReply{Error: "Sharing stopped, but model memory release needs attention."}
	u.stopAll()
	if r := awaitCall(t, h); r["action"] != "stop_all" {
		t.Fatal(r)
	}
	u.stopAll()
	if len(h.calls) != 0 {
		t.Fatal("duplicate stop request")
	}
	close(h.release)
	u.pending.Wait()
	if !strings.Contains(u.errText, "memory release needs attention") {
		t.Fatal("stop failure erased by refresh")
	}
	if !u.memoryReleasePending["fixture"] {
		t.Fatal("refresh did not apply pending release state")
	}
	if u.working() {
		t.Fatal("stop remained busy")
	}
}
