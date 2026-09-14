package flatpak

import (
	"context"
	"os"
	"time"
)

// Flatpak writes files/.updated into the previous deployment when a newer
// version is installed, which a running sandbox sees as /app/.updated.
var updateMarker = "/app/.updated"

// WatchUpdate calls notify once when a newer version has been installed while
// this process is running. It returns after notifying or when ctx ends.
func WatchUpdate(ctx context.Context, interval time.Duration, notify func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(updateMarker); err == nil {
			notify()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
