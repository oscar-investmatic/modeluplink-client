package main

import (
	"strings"
	"testing"
)

func TestTrayStopKeepsMemoryReleaseFailureVisible(t *testing.T) {
	u, h := newTestUI(t)
	h.reply = helperReply{Notice: "Your sharing has stopped, but model memory still needs releasing.", Stopped: map[string]bool{"test": true}, MemoryReleasePending: map[string]bool{"test": true}}
	u.stopAll()
	if r := awaitCall(t, h); r["action"] != "stop_all" {
		t.Fatal("tray did not request Stop")
	}
	close(h.release)
	u.pending.Wait()
	if !strings.Contains(u.errText, "memory") || !u.paused["test"] || !u.memoryReleasePending["test"] {
		t.Fatal("stop state or release error was lost on refresh")
	}
	if strings.Contains(u.message, "memory released") {
		t.Fatal("reported success after unload failed")
	}
}
