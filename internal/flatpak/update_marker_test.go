package flatpak

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchUpdateNotifiesOnceWhenMarkerAppears(t *testing.T) {
	updateMarker = filepath.Join(t.TempDir(), ".updated")
	t.Cleanup(func() { updateMarker = "/app/.updated" })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	notified := make(chan struct{}, 2)
	done := make(chan struct{})
	go func() { WatchUpdate(ctx, 10*time.Millisecond, func() { notified <- struct{}{} }); close(done) }()
	select {
	case <-notified:
		t.Fatal("notified before an update was installed")
	case <-time.After(50 * time.Millisecond):
	}
	if err := os.WriteFile(updateMarker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("installed update not noticed")
	}
	if len(notified) != 1 {
		t.Fatalf("notified %d times", len(notified))
	}
}

func TestWatchUpdateStopsWithContext(t *testing.T) {
	updateMarker = filepath.Join(t.TempDir(), ".updated")
	t.Cleanup(func() { updateMarker = "/app/.updated" })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		WatchUpdate(ctx, 10*time.Millisecond, func() { t.Error("unexpected notification") })
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher ignored cancellation")
	}
}
