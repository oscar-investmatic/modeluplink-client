//go:build !windows

package service

import (
	"fmt"
	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
)

// RetireCurrent is called by the agent itself after tunnel cancellation and
// model unload. Remove auto-start first; stopping the unit can kill the caller.
func RetireCurrent(slug string) error {
	if flatpak.Enabled() {
		return flatpak.Call("Retire", slug)
	}
	if err := security.ValidateSlug(slug); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		path := filepath.Join(home, "Library", "LaunchAgents", "com.modeluplink.agent."+slug+".plist")
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return exec.Command("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/com.modeluplink.agent."+slug).Run()
	case "linux":
		unit := "modeluplink-" + slug + ".service"
		if err = exec.Command("systemctl", "--user", "disable", unit).Run(); err != nil {
			return err
		}
		path := filepath.Join(home, ".config", "systemd", "user", unit)
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return exec.Command("systemctl", "--user", "stop", "--no-block", unit).Run()
	default:
		return fmt.Errorf("unsupported service platform")
	}
}
