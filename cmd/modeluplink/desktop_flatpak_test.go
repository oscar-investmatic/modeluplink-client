package main

import (
	"fmt"
	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
	"testing"
)

func TestDesktopPreservesFlatpakPermissionDenial(t *testing.T) {
	message := "Background activity was denied. Allow it in your desktop’s application permissions to start sharing."
	err := fmt.Errorf("install background service (endpoint rolled back): %w", &flatpak.UserError{Message: message})
	if got := desktopError(err); got != message {
		t.Fatalf("permission remedy hidden: %q", got)
	}
}
