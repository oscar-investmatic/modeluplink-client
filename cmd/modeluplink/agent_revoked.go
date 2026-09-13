package main

import (
	"context"
	"github.com/oscar-investmatic/modeluplink-client/internal/engine"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/internal/service"
	"log/slog"
)

var retireCurrentService = service.RetireCurrent

// All tunnel requests have been cancelled before this runs. Save a durable
// stop marker before releasing memory, then remove auto-start before stopping
// our own unit (which can terminate this process before the call returns).
func retireRevokedEndpoint(ctx context.Context, e localconfig.Endpoint) error {
	e.Revoked, e.Stopped = true, true
	e.MemoryReleasePending = e.ManagesRuntime()
	if _, err := localconfig.SaveEndpoint(e); err != nil {
		slog.Error("could not save revoked connection state")
	}
	if e.ManagesRuntime() {
		if err := engine.UnloadModels(ctx, e.UpstreamURL, e.SharedModels); err == nil {
			e.MemoryReleasePending = false
			_, _ = localconfig.SaveEndpoint(e)
		} else {
			slog.Warn("sharing stopped; model memory release not confirmed")
		}
	}
	if err := retireCurrentService(e.Slug); err != nil {
		slog.Warn("could not remove revoked service auto-start")
	}
	// Do not exit/reconnect in a restart loop if the service manager is unavailable.
	<-ctx.Done()
	return nil
}
