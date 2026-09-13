//go:build !windows

package service

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
)

func mockServiceCommand(t *testing.T, run func(string, ...string) ([]byte, error)) {
	t.Helper()
	previous := serviceCommand
	t.Cleanup(func() { serviceCommand = previous })
	serviceCommand = run
}
func TestStartupOnlyChangesFutureActivation(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			var calls []string
			mockServiceCommand(t, func(name string, args ...string) ([]byte, error) {
				calls = append(calls, name+" "+strings.Join(args, " "))
				return nil, nil
			})
			targets := []startupTarget{{"active", false}, {"stopped", true}}
			if err := updateStartup(platform, targets, true, false); err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 || !strings.Contains(calls[0], "enable") || !strings.Contains(calls[1], "disable") {
				t.Fatal(calls)
			}
			for _, call := range calls {
				for _, forbidden := range []string{"--now", "bootstrap", "kickstart", "bootout", " start ", " stop ", "restart"} {
					if strings.Contains(call, forbidden) {
						t.Fatal("startup change interrupted or restarted sharing", call)
					}
				}
			}
		})
	}
}
func TestStartupFailureRestoresPreviousPreference(t *testing.T) {
	var calls []string
	mockServiceCommand(t, func(name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if len(calls) == 2 {
			return nil, errors.New("permission denied")
		}
		return nil, nil
	})
	err := updateStartup("linux", []startupTarget{{"first", false}, {"second", false}}, false, true)
	if err == nil {
		t.Fatal("failure hidden")
	}
	want := []string{"--user disable first", "--user disable second", "--user enable first", "--user enable second"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatal(calls)
	}
}
func TestStartupInventoryProtectsStoppedAndIndependentServices(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
			home := t.TempDir()
			dir := filepath.Join(home, ".config", "systemd", "user")
			filename := func(slug string) string { return "modeluplink-" + slug + ".service" }
			runtimeFile := "modeluplink-ollama.service"
			if platform == "darwin" {
				dir = filepath.Join(home, "Library", "LaunchAgents")
				filename = func(slug string) string { return "com.modeluplink.agent." + slug + ".plist" }
				runtimeFile = "com.modeluplink.ollama.plist"
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			cfg := localconfig.Config{Endpoints: map[string]localconfig.Endpoint{}}
			for _, slug := range []string{"active", "stopped", "revoked", "removed"} {
				e := localconfig.Endpoint{ID: slug, Slug: slug, Stopped: slug == "stopped"}
				cfg.Endpoints[slug] = e
				// Revocation is written to the endpoint file before the account config.
				e.Revoked = slug == "revoked"
				if _, err := localconfig.SaveEndpoint(e); err != nil {
					t.Fatal(err)
				}
				if slug != "removed" {
					if err := os.WriteFile(filepath.Join(dir, filename(slug)), []byte("owned fixture"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, file := range []string{runtimeFile, "ollama.service", "com.other.app.plist", filename("untracked")} {
				if err := os.WriteFile(filepath.Join(dir, file), []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			targets, err := startupTargets(platform, home, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(targets) != 4 {
				t.Fatal(targets)
			}
			for _, target := range targets {
				stopped := strings.Contains(target.name, "stopped") || strings.Contains(target.name, "revoked")
				if target.stopped != stopped {
					t.Fatal("lost newest stop/revocation state", target)
				}
			}
			// A replaced service file must not allow us to enable another app's unit.
			if err := os.Remove(filepath.Join(dir, filename("active"))); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(dir, "ollama.service"), filepath.Join(dir, filename("active"))); err != nil {
				t.Fatal(err)
			}
			if _, err := startupTargets(platform, home, cfg); err == nil {
				t.Fatal("accepted a replaced service file")
			}
		})
	}
}
func TestManualStartKeepsDisabledAutomaticPreference(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			var calls []string
			mockServiceCommand(t, func(name string, args ...string) ([]byte, error) {
				calls = append(calls, name+" "+strings.Join(args, " "))
				if name == "launchctl" && args[0] == "print" {
					return nil, errors.New("not loaded")
				}
				return nil, nil
			})
			var err error
			if platform == "linux" {
				err = startSystemd("fixture.service", false)
			} else {
				err = startLaunchAgent("gui/fixture", "com.modeluplink.fixture", "/fixture.plist", false)
			}
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(calls, "\n")
			if platform == "linux" {
				if !reflect.DeepEqual(calls, []string{"systemctl --user daemon-reload", "systemctl --user disable fixture.service", "systemctl --user start fixture.service"}) {
					t.Fatal(calls)
				}
			} else {
				if len(calls) != 5 || !strings.Contains(calls[2], "enable") || !strings.Contains(calls[3], "bootstrap") || !strings.Contains(calls[4], "disable") {
					t.Fatal(joined)
				}
			}
		})
	}
}
func TestFailedMacPreferenceRestoreDoesNotLeaveAutostartFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.plist")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	mockServiceCommand(t, func(name string, args ...string) ([]byte, error) {
		if args[0] == "print" || args[0] == "disable" {
			return nil, errors.New("failure")
		}
		return nil, nil
	})
	if err := startLaunchAgent("gui/fixture", "com.modeluplink.fixture", path, false); err == nil {
		t.Fatal("failure hidden")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("autostart file left behind")
	}
}
