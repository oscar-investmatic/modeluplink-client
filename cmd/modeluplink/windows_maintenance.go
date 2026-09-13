//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/hostexec"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/internal/service"
	"github.com/oscar-investmatic/modeluplink-client/pkg/client"
	"github.com/zalando/go-keyring"
	"golang.org/x/sys/windows"
)

// Installer commands use the current user's protected configuration. They never
// take account credentials on the command line or remove Ollama model files.
func platformCommand(command string) (bool, error) {
	if command != "_prepare-update" && command != "_finish-update" && command != "_uninstall" {
		return false, nil
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return true, err
	}
	executable, err := os.Executable()
	if err != nil {
		return true, err
	}
	if command == "_finish-update" {
		pending := append([]string{}, cfg.UpgradeResume...)
		for _, slug := range pending {
			e, ok := cfg.Endpoints[slug]
			if !ok {
				continue
			}
			path, err := localconfig.EndpointPath(slug)
			if err != nil {
				return true, err
			}
			e, err = localconfig.LoadEndpoint(path)
			if err != nil {
				return true, err
			}
			if e.Revoked {
				continue
			}
			e.Stopped = false
			if _, err = localconfig.SaveEndpoint(e); err != nil {
				return true, err
			}
			if _, err = service.Install(executable, path, slug); err != nil {
				e.Stopped = true
				_, _ = localconfig.SaveEndpoint(e)
				return true, err
			}
			cfg.Endpoints[slug] = e
		}
		cfg.UpgradeResume = nil
		return true, localconfig.Save(cfg)
	}
	if command == "_prepare-update" {
		for _, e := range cfg.Endpoints {
			path, err := localconfig.EndpointPath(e.Slug)
			if err != nil {
				return true, err
			}
			if saved, err := localconfig.LoadEndpoint(path); err == nil {
				e = saved
			}
			if !e.Stopped && !e.Revoked && !contains(cfg.UpgradeResume, e.Slug) {
				cfg.UpgradeResume = append(cfg.UpgradeResume, e.Slug)
			}
		}
		if err = localconfig.Save(cfg); err != nil {
			return true, err
		}
	}
	for _, e := range cfg.Endpoints {
		path, err := localconfig.EndpointPath(e.Slug)
		if err != nil {
			return true, err
		}
		if saved, err := localconfig.LoadEndpoint(path); err == nil {
			e = saved
		}
		reply, err := stopSharingForMaintenance(cfg, e, nil, command == "_prepare-update")
		if err != nil {
			return true, err
		}
		if reply.MemoryReleasePending[e.Slug] {
			return true, errors.New("model memory is still in use; stop sharing in the app before continuing")
		}
	}
	if err = service.StopManagedOllama(); err != nil {
		return true, err
	}
	if err = service.StopInterface(); err != nil {
		return true, err
	}
	if command == "_prepare-update" {
		return true, nil
	}
	if cfg.AccountToken != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.New(cfg.ControlURL, cfg.AccountToken).SignOut(ctx)
	}
	if err = keyring.DeleteAll("Model Uplink"); err != nil && !errors.Is(err, keyring.ErrNotFound) && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return true, err
	}
	for slug := range cfg.Endpoints {
		if err = localconfig.DeleteEndpointFiles(slug); err != nil {
			return true, err
		}
	}
	path, err := localconfig.Path()
	if err != nil {
		return true, err
	}
	for _, name := range []string{filepath.Base(path), "ollama-runtime.json"} {
		if err = os.Remove(filepath.Join(filepath.Dir(path), name)); err != nil && !os.IsNotExist(err) {
			return true, err
		}
	}
	return true, nil
}

func followWindowsLog(path string) error {
	data, err := json.Marshal(path)
	if err != nil {
		return err
	}
	cmd := hostexec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `[Console]::InputEncoding=[Text.UTF8Encoding]::new($false);$p=[Console]::In.ReadToEnd() | ConvertFrom-Json;Get-Content -LiteralPath $p -Tail 30 -Wait`)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
func stopWindowsEndpoint(slug string) error {
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	if _, ok := cfg.Endpoints[slug]; !ok {
		return errors.New("endpoint is not configured on this computer")
	}
	path, err := localconfig.EndpointPath(slug)
	if err != nil {
		return err
	}
	e, err := localconfig.LoadEndpoint(path)
	if err != nil {
		return err
	}
	reply, err := desktopStopSharing(cfg, e, nil)
	if err != nil {
		return err
	}
	if reply.MemoryReleasePending[slug] {
		return errors.New(reply.Notice)
	}
	fmt.Println("Sharing stopped. Model memory released.")
	return nil
}
