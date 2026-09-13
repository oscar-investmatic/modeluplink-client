package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/hostexec"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
)

func windowsOllamaPaths() []string {
	base := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(base) {
		return nil
	}
	return []string{filepath.Join(base, "Programs", "Ollama", "ollama.exe"), filepath.Join(base, "Model Uplink", "ollama", ollamaVersion, "ollama.exe")}
}
func installWindows(ctx context.Context) error {
	if runtime.GOARCH != "amd64" {
		return errors.New("Windows supports x86-64 computers only")
	}
	artifact, ok := ollamaArtifacts["windows/amd64"]
	if !ok {
		return errors.New("Windows runtime is not configured")
	}
	base := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(base) {
		return errors.New("Windows local application storage is unavailable")
	}
	root := filepath.Join(base, "Model Uplink", "ollama")
	if err := localconfig.PrivateDirectory(root); err != nil {
		return err
	}
	target := filepath.Join(root, ollamaVersion)
	if info, err := os.Stat(filepath.Join(target, "ollama.exe")); err == nil && !info.IsDir() {
		return nil
	}
	archive, err := downloadVerifiedFile(ctx, artifact)
	if err != nil {
		return err
	}
	defer os.Remove(archive)
	stage, err := os.MkdirTemp(root, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = extractWindowsRuntime(archive, stage); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(stage, "ollama.exe")); err != nil {
		return errors.New("verified runtime does not contain ollama.exe")
	}
	return os.Rename(stage, target)
}
func verifyWindowsLoopback() error {
	script := `$ErrorActionPreference='Stop'; @(Get-NetTCPConnection -State Listen -LocalPort 11434 | Select-Object -ExpandProperty LocalAddress) | ConvertTo-Json -Compress`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := hostexec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return errors.New("could not verify that Ollama is listening only on this computer")
	}
	var addresses []string
	if json.Unmarshal(output, &addresses) != nil {
		var one string
		if json.Unmarshal(output, &one) != nil {
			return errors.New("could not read Ollama listening addresses")
		}
		addresses = []string{one}
	}
	if len(addresses) == 0 {
		return errors.New("Ollama has no verified local listener")
	}
	for _, ip := range addresses {
		if strings.TrimSpace(ip) != "127.0.0.1" && strings.TrimSpace(ip) != "::1" {
			return errors.New("Ollama must listen only on 127.0.0.1 or ::1 before sharing")
		}
	}
	return nil
}
