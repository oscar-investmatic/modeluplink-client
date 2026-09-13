//go:build !windows

package service

import (
	"errors"
	"fmt"
	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"

	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
)

type startupTarget struct {
	name    string
	stopped bool
}

// SetStartup changes future activation only. It never starts, stops, or
// recreates a connection, and never changes independently managed Ollama.
func SetStartup(enabled bool) error {
	if flatpak.Enabled() {
		return flatpak.Call("Startup", enabled)
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	targets, err := startupTargets(runtime.GOOS, home, cfg)
	if err != nil {
		return err
	}
	return updateStartup(runtime.GOOS, targets, enabled, !cfg.StartupDisabled)
}

func startupTargets(platform, home string, cfg localconfig.Config) ([]startupTarget, error) {
	var directory, runtimeName string
	switch platform {
	case "darwin":
		directory, runtimeName = filepath.Join(home, "Library", "LaunchAgents"), "com.modeluplink.ollama.plist"
	case "linux":
		directory, runtimeName = filepath.Join(home, ".config", "systemd", "user"), "modeluplink-ollama.service"
	default:
		return nil, errors.New("Automatic startup is unavailable on this operating system.")
	}
	var targets []startupTarget
	add := func(file, name string, stopped bool) error {
		info, err := os.Lstat(filepath.Join(directory, file))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		} // Explicit Stop removes the service.
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("A background-service file has been replaced. Please reinstall Model Uplink.")
		}
		for i := range targets {
			if targets[i].name == name {
				targets[i].stopped = targets[i].stopped || stopped
				return nil
			}
		}
		targets = append(targets, startupTarget{name, stopped})
		return nil
	}
	for slug, e := range cfg.Endpoints {
		if err := security.ValidateSlug(slug); err != nil {
			return nil, err
		}
		path, err := localconfig.EndpointPath(slug)
		if err != nil {
			return nil, err
		}
		saved, err := localconfig.LoadEndpoint(path)
		if err == nil {
			if saved.ID != e.ID || saved.Slug != slug {
				return nil, errors.New("Your saved connection settings do not match. Please contact support.")
			}
			e = saved
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		name := "modeluplink-" + slug + ".service"
		file := name
		if platform == "darwin" {
			name = "com.modeluplink.agent." + slug
			file = name + ".plist"
		}
		if err := add(file, name, e.Stopped || e.Revoked); err != nil {
			return nil, err
		}
	}
	name := runtimeName
	if platform == "darwin" {
		name = "com.modeluplink.ollama"
	}
	if err := add(runtimeName, name, false); err != nil {
		return nil, err
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].name < targets[j].name })
	return targets, nil
}

func updateStartup(platform string, targets []startupTarget, enabled, previous bool) error {
	for i, t := range targets {
		if err := setServiceStartup(platform, t.name, enabled && !t.stopped); err != nil {
			failures := []error{err}
			// Include the failed operation: an OS command may change state before it
			// reports failure. Restore the previously saved app preference everywhere.
			for _, changed := range targets[:i+1] {
				if rollback := setServiceStartup(platform, changed.name, previous && !changed.stopped); rollback != nil {
					failures = append(failures, rollback)
				}
			}
			return errors.Join(failures...)
		}
	}
	return nil
}

func setServiceStartup(platform, name string, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var err error
	switch platform {
	case "darwin":
		_, err = serviceCommand("launchctl", action, "gui/"+strconv.Itoa(os.Getuid())+"/"+name)
	case "linux":
		_, err = serviceCommand("systemctl", "--user", action, name)
	default:
		return errors.New("Automatic startup is unavailable on this operating system.")
	}
	if err != nil {
		return fmt.Errorf("Couldn’t update automatic startup for Model Uplink: %w", err)
	}
	return nil
}

// A manual Start is allowed even with automatic startup disabled. launchd's
// disabled override prevents bootstrap, so restore it only after loading the
// current session's job. Disabling a loaded job does not stop it.
func startLaunchAgent(target, label, path string, automatic bool) error {
	_, _ = serviceCommand("launchctl", "bootout", target+"/"+label)
	if err := waitLaunchAgentGone(target + "/" + label); err != nil {
		return err
	}
	if err := setServiceStartup("darwin", label, true); err != nil {
		return err
	}
	_, startErr := serviceCommand("launchctl", "bootstrap", target, path)
	if !automatic {
		if err := setServiceStartup("darwin", label, false); err != nil {
			// Never leave a newly installed automatic job behind against the user's
			// saved preference when restoring the launchd override fails.
			_, stopErr := serviceCommand("launchctl", "bootout", target+"/"+label)
			removeErr := os.Remove(path)
			return errors.Join(startErr, err, stopErr, removeErr)
		}
	}
	if startErr != nil {
		return fmt.Errorf("Couldn’t start background sharing: %w", startErr)
	}
	return nil
}

func startSystemd(unit string, automatic bool) error {
	if _, err := serviceCommand("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("Couldn’t reload background services: %w", err)
	}
	if err := setServiceStartup("linux", unit, automatic); err != nil {
		return err
	}
	if _, err := serviceCommand("systemctl", "--user", "start", unit); err != nil {
		return fmt.Errorf("Couldn’t start background sharing: %w", err)
	}
	return nil
}
