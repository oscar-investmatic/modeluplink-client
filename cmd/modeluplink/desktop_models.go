package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"

	"github.com/oscar-investmatic/modeluplink-client/internal/engine"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/internal/service"
	"github.com/oscar-investmatic/modeluplink-client/internal/upstream"
)

func desktopModels(request desktopRequest) ([]string, error) {
	// New clients encode arrays inside the string-only helper request contract.
	// Retain the old comma-separated field solely for older bundled clients.
	names := []string{request.Model}
	if request.ModelIDs != "" {
		if err := json.Unmarshal([]byte(request.ModelIDs), &names); err != nil {
			return nil, errors.New("Choose valid model IDs to share.")
		}
	} else if request.Models != "" {
		names = strings.Split(request.Models, ",")
		for i := range names {
			names[i] = strings.TrimSpace(names[i])
		}
	}
	if len(names) == 0 {
		return nil, errors.New("Choose at least one model to share.")
	}
	var selected []string
	for _, name := range names {
		if strings.TrimSpace(name) == "" || len(name) > 200 || strings.ContainsAny(name, "\x00\r\n") {
			return nil, errors.New("Choose valid model IDs to share.")
		}
		if !contains(selected, name) {
			selected = append(selected, name)
		}
	}
	if len(selected) > 8 {
		return nil, errors.New("Choose at most eight models to share.")
	}
	return selected, nil
}
func desktopShareModels(request desktopRequest, cfg localconfig.Config, local localconfig.Endpoint, report setupReporter) (desktopResponse, error) {
	var out desktopResponse
	selected, err := desktopModels(request)
	if err != nil {
		return out, err
	}
	key, err := localconfig.ResolveUpstreamKey(local)
	if err != nil {
		return out, err
	}
	source := upstream.Connection{URL: local.UpstreamURL, Key: key, AllowLAN: local.AllowLAN}
	models, err := source.Models(context.Background())
	if err != nil {
		return out, err
	}
	for _, name := range selected {
		if !contains(models, name) {
			return out, errors.New("Choose models already installed on this Mac.")
		}
	}
	// Updating sharing intentionally reconnects this endpoint. Keep the address,
	// key and certificate, and restore the prior configuration if validation fails.
	path, err := localconfig.EndpointPath(local.Slug)
	if err != nil {
		return out, err
	}
	executable, err := os.Executable()
	if err != nil {
		return out, err
	}
	if err = service.StopSharing(local.Slug); err != nil && !local.Stopped {
		return out, errors.New("We couldn’t stop the current connection to update its models.")
	}
	restored := false
	defer func() {
		if !restored && !local.Stopped {
			_, _ = installService(executable, path, local.Slug)
		}
	}()
	if local.ManagesRuntime() {
		if err = engine.ConfigureManagedOllama(context.Background()); err != nil {
			return out, setupError("prepare", err)
		}
		for _, name := range selected {
			report.stage("modelcheck", "Checking that "+name+" can answer on this Mac…")
			if err = engine.CheckModel(context.Background(), local.UpstreamURL, name); err != nil {
				return out, setupError("modelcheck", err)
			}
		}
	} else {
		for _, name := range selected {
			if err = source.Test(context.Background(), name); err != nil {
				return out, err
			}
		}
	}
	previous := local
	local.SharedModels = selected
	local.Stopped = false
	local.MemoryReleasePending = false
	if _, err = localconfig.SaveEndpoint(local); err != nil {
		return out, err
	}
	cfg.Endpoints[local.Slug] = local
	if err = localconfig.Save(cfg); err != nil {
		_, _ = localconfig.SaveEndpoint(previous)
		return out, err
	}
	report.stage("start", "Applying shared models and reconnecting…")
	if _, err = installService(executable, path, local.Slug); err != nil {
		cfg.Endpoints[local.Slug] = previous
		_ = localconfig.Save(cfg)
		_, _ = localconfig.SaveEndpoint(previous)
		return out, errors.New("We couldn’t restart the connection. Your previous shared models were restored.")
	}
	restored = true
	out.SharedModels = map[string][]string{local.Slug: selected}
	return out, nil
}

func desktopStopSharing(cfg localconfig.Config, local localconfig.Endpoint, report setupReporter) (desktopResponse, error) {
	return stopSharingForMaintenance(cfg, local, report, false)
}

func stopSharingForMaintenance(cfg localconfig.Config, local localconfig.Endpoint, report setupReporter, preserveUpgradeResume bool) (desktopResponse, error) {
	var out desktopResponse
	if !preserveUpgradeResume {
		pending := make([]string, 0, len(cfg.UpgradeResume))
		for _, slug := range cfg.UpgradeResume {
			if slug != local.Slug {
				pending = append(pending, slug)
			}
		}
		cfg.UpgradeResume = pending
	}
	alreadyReleased := local.Stopped && !local.MemoryReleasePending
	report.stage("stop", "Stopping shared requests…")
	if runtime.GOOS == "windows" {
		local.Stopped = true
		local.MemoryReleasePending = local.ManagesRuntime() && !alreadyReleased
		if _, err := localconfig.SaveEndpoint(local); err != nil {
			return out, err
		}
		cfg.Endpoints[local.Slug] = local
		if err := localconfig.Save(cfg); err != nil {
			return out, err
		}
	}
	if err := service.StopSharing(local.Slug); err != nil {
		return out, errors.New("We couldn’t stop the connection. Please try again.")
	}
	local.Stopped = true
	local.MemoryReleasePending = local.ManagesRuntime() && !alreadyReleased
	save := func() error {
		cfg.Endpoints[local.Slug] = local
		if _, err := localconfig.SaveEndpoint(local); err != nil {
			return err
		}
		return localconfig.Save(cfg)
	}
	if err := save(); err != nil {
		return out, err
	}
	if local.ManagesRuntime() && !alreadyReleased {
		report.stage("unload", "Releasing model memory…")
		if err := engine.UnloadModels(context.Background(), local.UpstreamURL, local.SharedModels); err != nil {
			out.Notice = "Sharing has stopped, but Ollama hasn’t confirmed that model memory was released. Close any other app using the model, then choose Release model memory."
		} else {
			local.MemoryReleasePending = false
			if err = save(); err != nil {
				return out, err
			}
		}
	}
	out.Stopped = map[string]bool{local.Slug: true}
	out.MemoryReleasePending = map[string]bool{local.Slug: local.MemoryReleasePending}
	return out, nil
}
