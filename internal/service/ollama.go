//go:build !windows

package service

import (
	"fmt"
	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

var serviceCommand = func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).CombinedOutput() }

func ensureLinger() error {
	uid := strconv.Itoa(os.Getuid())
	output, err := serviceCommand("loginctl", "show-user", uid, "--property=Linger", "--value")
	if err == nil && strings.TrimSpace(string(output)) == "yes" {
		return nil
	}
	if output, err = serviceCommand("loginctl", "enable-linger", uid); err != nil {
		return fmt.Errorf("enable background services after logout/reboot: %s; run sudo loginctl enable-linger %s, then retry", strings.TrimSpace(string(output)), uid)
	}
	return nil
}

// InstallOllama installs one shared user service. Existing independently
// managed Ollama servers are left alone by the caller; model files are retained.
func InstallOllama(executable string) (string, error) {
	if flatpak.Enabled() {
		return "", fmt.Errorf("Use a separately installed model server with the Flatpak edition.")
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	models := os.Getenv("OLLAMA_MODELS")
	if models == "" {
		models = filepath.Join(home, ".ollama", "models")
	}
	if models, err = filepath.Abs(models); err != nil {
		return "", err
	}
	if err = os.MkdirAll(models, 0700); err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "linux":
		if err = ensureLinger(); err != nil {
			return "", err
		}
		dir := filepath.Join(home, ".config", "systemd", "user")
		if err = os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
		unit := "modeluplink-ollama.service"
		if err = os.WriteFile(filepath.Join(dir, unit), []byte(ollamaSystemdUnit(executable, models)), 0600); err != nil {
			return "", err
		}
		if err := startSystemd(unit, !cfg.StartupDisabled); err != nil {
			return "", err
		}
		return "journalctl --user -u " + unit, nil
	case "darwin":
		dir := filepath.Join(home, "Library", "LaunchAgents")
		logs := filepath.Join(home, "Library", "Logs", "Model Uplink")
		for _, path := range []string{dir, logs} {
			if err = os.MkdirAll(path, 0700); err != nil {
				return "", err
			}
		}
		label := "com.modeluplink.ollama"
		log := filepath.Join(logs, "ollama.log")
		plist := filepath.Join(dir, label+".plist")
		if err = os.WriteFile(plist, []byte(ollamaPlist(executable, models, log)), 0600); err != nil {
			return "", err
		}
		target := "gui/" + strconv.Itoa(os.Getuid())
		if err := startLaunchAgent(target, label, plist, !cfg.StartupDisabled); err != nil {
			return "", err
		}
		return log, nil
	default:
		return "", fmt.Errorf("managed Ollama service is unsupported on %s", runtime.GOOS)
	}
}

func ollamaSystemdUnit(executable, models string) string {
	return fmt.Sprintf(`[Unit]
Description=Model Uplink managed Ollama
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s serve
Environment="OLLAMA_HOST=127.0.0.1:11434"
Environment="OLLAMA_MAX_LOADED_MODELS=1"
Environment="OLLAMA_NUM_PARALLEL=1"
Environment="OLLAMA_MAX_QUEUE=4"
Environment=%s
Restart=always
RestartSec=5
NoNewPrivileges=true
LimitCORE=0

[Install]
WantedBy=default.target
`, systemdQuote(executable), systemdQuote("OLLAMA_MODELS="+models))
}

func ollamaPlist(executable, models, log string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.modeluplink.ollama</string>
<key>ProgramArguments</key><array><string>%s</string><string>serve</string></array>
<key>EnvironmentVariables</key><dict><key>OLLAMA_HOST</key><string>127.0.0.1:11434</string><key>OLLAMA_MODELS</key><string>%s</string><key>OLLAMA_MAX_LOADED_MODELS</key><string>1</string><key>OLLAMA_NUM_PARALLEL</key><string>1</string><key>OLLAMA_MAX_QUEUE</key><string>4</string></dict>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>5</integer>
<key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, xml(executable), xml(models), xml(log), xml(log))
}

// RefreshManagedOllama updates only a service previously installed by us.
// Independently managed Ollama instances retain their own settings.
func RefreshManagedOllama(executable string) (bool, error) {
	if flatpak.Enabled() {
		return false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	var path string
	switch runtime.GOOS {
	case "darwin":
		path = filepath.Join(home, "Library", "LaunchAgents", "com.modeluplink.ollama.plist")
	case "linux":
		path = filepath.Join(home, ".config", "systemd", "user", "modeluplink-ollama.service")
	default:
		return false, nil
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.Contains(string(content), "OLLAMA_MAX_LOADED_MODELS") {
		return false, nil
	}
	_, err = InstallOllama(executable)
	if err == nil && runtime.GOOS == "linux" {
		_, err = serviceCommand("systemctl", "--user", "restart", "modeluplink-ollama.service")
	}
	return err == nil, err
}
