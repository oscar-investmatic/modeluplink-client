package main

import (
	"fmt"
	"testing"

	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
)

func TestDesktopPreservesFlatpakPermissionDenial(t *testing.T) {
	message := "Model Uplink isn’t allowed to run in the background. Choose Allow if your desktop asks, or turn on background activity for Model Uplink in your desktop’s app settings, then start sharing again."
	err := fmt.Errorf("install background service (endpoint rolled back): %w", &flatpak.UserError{Message: message})
	if got := desktopError(err); got != message {
		t.Fatalf("permission remedy hidden: %q", got)
	}
}
