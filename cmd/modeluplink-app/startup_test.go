package main

import (
	"fyne.io/fyne/v2/widget"
	"testing"
)

func TestStartupToggleWaitsForHelperAndRestoresOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "failed"}[fail], func(t *testing.T) {
			u, h := newTestUI(t)
			u.startupEnabled = true
			disabled := false
			h.reply = helperReply{StartupEnabled: &disabled}
			if fail {
				h.reply = helperReply{Error: "Startup could not be changed."}
			}
			problem := widget.NewLabel("")
			control := u.startupControl(problem)
			control.SetChecked(false)
			request := awaitCall(t, h)
			if request["action"] != "startup" || request["enabled"] != "false" {
				t.Fatal("wrong startup request")
			}
			if !control.Disabled() || !u.startupEnabled {
				t.Fatal("setting changed before helper confirmation")
			}
			close(h.release)
			u.pending.Wait()
			if control.Disabled() || control.Checked != fail || u.startupEnabled != fail {
				t.Fatal("wrong saved setting")
			}
			if fail && problem.Text != h.reply.Error {
				t.Fatal("startup failure hidden by dialog")
			}
		})
	}
}
