package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPreviewUpdateRecoveryIsActionableAndDoesNotExposeRemoteErrors(t *testing.T) {
	for _, err := range []error{
		dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownMethod"},
		dbus.NewError("org.freedesktop.DBus.Error.UnknownMethod", nil),
	} {
		if !strings.Contains(updateRecoveryMessage(err), "sign out of Linux") {
			t.Fatal("preview without a handover method has no graphical recovery path")
		}
	}
	message := updateRecoveryMessage(errors.New("private remote detail"))
	if strings.Contains(message, "private") || !strings.Contains(message, "Close the existing") {
		t.Fatal("unexpected errors should show bounded recovery instructions")
	}
}
