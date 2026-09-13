//go:build !windows

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"

	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
)

func Install(executable, configPath, slug string) (string, error) {
	if flatpak.Enabled() {
		if err := flatpak.Call("Start", slug); err != nil {
			return "", err
		}
		return flatpak.LogPath(slug)
	}
	if err := security.ValidateSlug(slug); err != nil {
		return "", err
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return installLaunchAgent(executable, configPath, slug, !cfg.StartupDisabled)
	case "linux":
		return installSystemd(executable, configPath, slug, !cfg.StartupDisabled)
	default:
		return "", fmt.Errorf("background service is unsupported on %s", runtime.GOOS)
	}
}

func Stop(slug string) error {
	if flatpak.Enabled() {
		return flatpak.Call("Stop", slug)
	}
	if err := security.ValidateSlug(slug); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		uid := strconv.Itoa(os.Getuid())
		label := "com.modeluplink.agent." + slug
		return exec.Command("launchctl", "bootout", "gui/"+uid+"/"+label).Run()
	case "linux":
		return exec.Command("systemctl", "--user", "disable", "--now", "modeluplink-"+slug+".service").Run()
	default:
		return fmt.Errorf("background service is unsupported on %s", runtime.GOOS)
	}
}

func Uninstall(slug string) error {
	if flatpak.Enabled() {
		return flatpak.Call("Stop", slug)
	}
	if err := security.ValidateSlug(slug); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	_ = Stop(slug)
	switch runtime.GOOS {
	case "darwin":
		path := filepath.Join(home, "Library", "LaunchAgents", "com.modeluplink.agent."+slug+".plist")
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	case "linux":
		path := filepath.Join(home, ".config", "systemd", "user", "modeluplink-"+slug+".service")
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if output, reloadErr := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); reloadErr != nil {
			return fmt.Errorf("systemctl daemon-reload: %s", strings.TrimSpace(string(output)))
		}
		return nil
	default:
		return fmt.Errorf("background service is unsupported on %s", runtime.GOOS)
	}
}

func LogPath(slug string) (string, error) {
	if flatpak.Enabled() {
		return flatpak.LogPath(slug)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Logs", "Model Uplink", slug+".log"), nil
	}
	return "journalctl --user -u modeluplink-" + slug + ".service", nil
}

func installLaunchAgent(executable, configPath, slug string, automatic bool) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	logDir := filepath.Join(home, "Library", "Logs", "Model Uplink")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err = os.MkdirAll(logDir, 0700); err != nil {
		return "", err
	}
	label := "com.modeluplink.agent." + slug
	plistPath := filepath.Join(dir, label+".plist")
	logPath := filepath.Join(logDir, slug+".log")
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>_agent</string><string>--config</string><string>%s</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string>
</dict></plist>`, xml(label), xml(executable), xml(configPath), xml(logPath), xml(logPath))
	if err = os.WriteFile(plistPath, []byte(plist), 0600); err != nil {
		return "", err
	}
	target := "gui/" + strconv.Itoa(os.Getuid())
	if err := startLaunchAgent(target, label, plistPath, automatic); err != nil {
		return "", err
	}
	return logPath, nil
}

func installSystemd(executable, configPath, slug string, automatic bool) (string, error) {
	if err := ensureLinger(); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	unit := "modeluplink-" + slug + ".service"
	unitPath := filepath.Join(dir, unit)
	certificateDir := filepath.Join(filepath.Dir(configPath), slug+"-certificates")
	if err = os.MkdirAll(certificateDir, 0700); err != nil {
		return "", err
	}
	content := systemdUnit(executable, configPath, certificateDir, slug)
	if err = os.WriteFile(unitPath, []byte(content), 0600); err != nil {
		return "", err
	}
	if err := startSystemd(unit, automatic); err != nil {
		return "", err
	}
	return "journalctl --user -u " + unit, nil
}

func systemdUnit(executable, configPath, certificateDir, slug string) string {
	return fmt.Sprintf(`[Unit]
Description=Model Uplink agent for %s
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s _agent --config %s
Restart=always
RestartSec=5
TimeoutStopSec=20
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%s

[Install]
WantedBy=default.target
`, slug, systemdQuote(executable), systemdQuote(configPath), systemdQuote(certificateDir))
}
func xml(value string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return r.Replace(value)
}
func systemdQuote(value string) string { return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"` }

// StopSharing verifies that the agent is gone before the app reports stopped.
func StopSharing(slug string) error {
	if flatpak.Enabled() {
		return flatpak.Call("Stop", slug)
	}
	if err := security.ValidateSlug(slug); err != nil {
		return err
	}
	_ = Stop(slug)
	switch runtime.GOOS {
	case "darwin":
		if err := waitLaunchAgentGone("gui/" + strconv.Itoa(os.Getuid()) + "/com.modeluplink.agent." + slug); err != nil {
			return err
		}
	case "linux":
		if exec.Command("systemctl", "--user", "is-active", "--quiet", "modeluplink-"+slug+".service").Run() == nil {
			return fmt.Errorf("connection agent is still running")
		}
	default:
		return fmt.Errorf("unsupported service platform")
	}
	return Uninstall(slug)
}

// launchctl bootout can return before the service registration disappears.
func waitLaunchAgentGone(target string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := serviceCommand("launchctl", "print", target); err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("background service is still stopping")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
