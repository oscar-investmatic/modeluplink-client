package engine

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
	"github.com/oscar-investmatic/modeluplink-client/internal/hostexec"
	"github.com/oscar-investmatic/modeluplink-client/internal/service"
)

const ollamaVersion = "0.30.8"

type ollamaArtifact struct {
	name, sha256 string
	maxBytes     int64
}

var ollamaArtifacts = map[string]ollamaArtifact{
	"windows/amd64": {"ollama-windows-amd64.zip", "c2d26d97e698027329c252629d7113bbc05d874b49960cbb03e93a39ae9fd95c", 2 << 30},
	"linux/amd64":   {"ollama-linux-amd64.tar.zst", "ffe2b2c2f2f5f5b30c081ec353c2e0bb2d9ead516064a8e22663b24b8fd8dca0", 2 << 30},
	"linux/arm64":   {"ollama-linux-arm64.tar.zst", "668a6f934b0b0455128bb4a76c9e50b9e5f274f9dc7710a066b7073e5bd36588", 2 << 30},
	"darwin/arm64":  {"ollama-darwin.tgz", "52acbca4e89c53db9abc586a22b5633fd101db293177264b9a0fe5d64a42a064", 512 << 20},
}

type Hardware struct {
	CPUs        int
	RAMBytes    uint64
	FreeBytes   uint64
	Accelerator string
}

func DetectHardware() Hardware {
	hardware := Hardware{CPUs: runtime.NumCPU(), RAMBytes: detectRAM(), FreeBytes: detectStorage()}
	if output, err := hostexec.Command("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader").Output(); err == nil {
		hardware.Accelerator = strings.TrimSpace(strings.Split(string(output), "\n")[0])
	} else if runtime.GOOS == "darwin" {
		if output, err = hostexec.Command("system_profiler", "SPDisplaysDataType", "-detailLevel", "mini").Output(); err == nil {
			for _, line := range strings.Split(string(output), "\n") {
				if strings.Contains(line, "Chipset Model:") {
					hardware.Accelerator = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Chipset Model:"))
					break
				}
			}
		}
	}
	if hardware.Accelerator == "" {
		hardware.Accelerator = "not detected"
	}
	return hardware
}

func (h Hardware) Summary() string {
	return fmt.Sprintf("%d CPU cores, %.1f GiB RAM, %.1f GiB free storage, accelerator: %s", h.CPUs, float64(h.RAMBytes)/(1<<30), float64(h.FreeBytes)/(1<<30), h.Accelerator)
}

func Probe(ctx context.Context, baseURL string) ([]string, error) {
	baseURL = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("model server returned %s", resp.Status)
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(result.Data))
	for _, model := range result.Data {
		models = append(models, model.ID)
	}
	return models, nil
}

func EnsureOllama(ctx context.Context, install bool) (string, error) {
	if flatpak.Enabled() {
		return "", errors.New("Install your model server separately, enable its API, then use Connect a model server.")
	}
	path, err := findOllama()
	if err != nil {
		if !install {
			return "", errors.New("Ollama is not installed; rerun without --no-install or install it from https://ollama.com/download")
		}
		if err = installOllama(ctx); err != nil {
			return "", err
		}
		path, err = findOllama()
		if err != nil {
			return "", errors.New("Ollama installation completed but the executable was not found")
		}
	}
	if _, probeErr := Probe(ctx, "http://127.0.0.1:11434"); probeErr == nil {
		if err = verifyOllamaLoopback(); err != nil {
			return "", err
		}
		return path, nil
	}
	logPath, err := service.InstallOllama(path)
	if err != nil {
		return "", err
	}
	for i := 0; i < 20; i++ {
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
		if _, err = Probe(ctx, "http://127.0.0.1:11434"); err == nil {
			if err = verifyOllamaLoopback(); err != nil {
				return "", err
			}
			return path, nil
		}
	}
	return "", fmt.Errorf("Ollama did not become ready; inspect %s", logPath)
}

func verifyOllamaLoopback() error {
	if runtime.GOOS == "windows" {
		return verifyWindowsLoopback()
	}
	var command *exec.Cmd
	kind := ""
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("ss"); err != nil {
			return nil
		}
		kind = "ss"
		command = hostexec.Command("ss", "-H", "-ltn", "sport = :11434")
	} else if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("lsof"); err != nil {
			return nil
		}
		kind = "lsof"
		command = hostexec.Command("lsof", "-nP", "-iTCP:11434", "-sTCP:LISTEN")
	} else {
		return nil
	}
	output, err := command.Output()
	if err != nil {
		return nil
	}
	if listenerOutputHasNonLoopback(kind, string(output)) {
		return errors.New("existing Ollama listens on a non-loopback address; bind it to 127.0.0.1:11434 before publishing it")
	}
	return nil
}

func listenerOutputHasNonLoopback(kind, output string) bool {
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "11434") {
			continue
		}
		fields := strings.Fields(line)
		local := ""
		switch kind {
		case "ss":
			if len(fields) >= 4 {
				local = fields[3]
			}
		case "lsof":
			for i, field := range fields {
				if field == "TCP" && i+1 < len(fields) {
					local = strings.Split(fields[i+1], "->")[0]
					break
				}
			}
		}
		host, port, err := net.SplitHostPort(local)
		if err != nil || port != "11434" {
			return true
		}
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return true
		}
	}
	return false
}

func UpdateOllama(ctx context.Context) error {
	fmt.Println("Updating Ollama to verified release", ollamaVersion)
	return installOllama(ctx)
}

func PullModel(ctx context.Context, ollamaPath, model string) error {
	cmd := hostexec.CommandContext(ctx, ollamaPath, "pull", model)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func findOllama() (string, error) {
	if path, err := exec.LookPath("ollama"); err == nil {
		return path, nil
	}
	home, _ := os.UserHomeDir()
	paths := []string{filepath.Join(home, ".local", "bin", "ollama")}
	if runtime.GOOS == "windows" {
		paths = windowsOllamaPaths()
	}
	if runtime.GOOS == "darwin" {
		paths = append(paths, "/Applications/Ollama.app/Contents/Resources/ollama", filepath.Join(home, "Applications", "Ollama.app", "Contents", "Resources", "ollama"))
	}
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}

func installOllama(ctx context.Context) error {
	switch runtime.GOOS {
	case "windows":
		return installWindows(ctx)
	case "linux":
		return installLinux(ctx)
	case "darwin":
		return installDarwin(ctx)
	default:
		return errors.New("automatic Ollama installation is supported on Linux and macOS")
	}
}

func installLinux(ctx context.Context) error {
	artifact := ollamaArtifacts["linux/"+runtime.GOARCH]
	if artifact.name == "" {
		return fmt.Errorf("automatic Ollama installation is unsupported on linux/%s", runtime.GOARCH)
	}
	archivePath, err := downloadVerifiedFile(ctx, artifact)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	installRoot := filepath.Join(home, ".local", "share", "modeluplink", "ollama")
	if err = os.MkdirAll(installRoot, 0700); err != nil {
		return err
	}
	versionDir := filepath.Join(installRoot, ollamaVersion)
	if info, statErr := os.Stat(filepath.Join(versionDir, "bin", "ollama")); statErr == nil && !info.IsDir() {
		return linkManagedOllama(home, versionDir)
	}
	if _, err = exec.LookPath("tar"); err != nil {
		return errors.New("verified Ollama installation requires tar with zstd support")
	}
	tempDir, err := os.MkdirTemp(installRoot, ".extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	cmd := hostexec.CommandContext(ctx, "tar", "--zstd", "-xf", archivePath, "-C", tempDir)
	if output, extractErr := cmd.CombinedOutput(); extractErr != nil {
		return fmt.Errorf("extract verified Ollama archive: %w: %s", extractErr, strings.TrimSpace(string(output)))
	}
	binary := filepath.Join(tempDir, "bin", "ollama")
	if info, statErr := os.Stat(binary); statErr != nil || info.IsDir() {
		return errors.New("verified Ollama archive did not contain bin/ollama")
	}
	if err = os.Rename(tempDir, versionDir); err != nil {
		return fmt.Errorf("install verified Ollama archive: %w", err)
	}
	if err = linkManagedOllama(home, versionDir); err != nil {
		return err
	}
	fmt.Println("Installed verified Ollama", ollamaVersion, "at", filepath.Join(versionDir, "bin", "ollama"))
	return nil
}

func linkManagedOllama(home, versionDir string) error {
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		return err
	}
	link := filepath.Join(binDir, "ollama")
	target := filepath.Join(versionDir, "bin", "ollama")
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("refusing to overwrite existing non-symlink %s", link)
		}
		existing, readErr := os.Readlink(link)
		managedRoot := filepath.Clean(filepath.Join(home, ".local", "share", "modeluplink")) + string(os.PathSeparator)
		if readErr != nil || !strings.HasPrefix(filepath.Clean(existing), managedRoot) {
			return fmt.Errorf("refusing to replace unmanaged symlink %s", link)
		}
		if err = os.Remove(link); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(target, link)
}

func installDarwin(ctx context.Context) error {
	artifact := ollamaArtifacts["darwin/"+runtime.GOARCH]
	if artifact.name == "" {
		return fmt.Errorf("automatic Ollama installation is unsupported on darwin/%s", runtime.GOARCH)
	}
	archivePath, err := downloadVerifiedFile(ctx, artifact)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	archive, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open verified Ollama archive: %w", err)
	}
	defer archive.Close()
	reader := tar.NewReader(archive)
	var binary []byte
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nextErr
		}
		if header.Typeflag == tar.TypeReg && filepath.Base(header.Name) == "ollama" {
			binary, err = io.ReadAll(io.LimitReader(reader, 512<<20))
			if err != nil {
				return err
			}
			break
		}
	}
	if len(binary) == 0 {
		return errors.New("verified Ollama archive did not contain the ollama executable")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	versionDir := filepath.Join(home, ".local", "share", "modeluplink", "ollama", ollamaVersion)
	binDir := filepath.Join(versionDir, "bin")
	if err = os.MkdirAll(binDir, 0700); err != nil {
		return err
	}
	target := filepath.Join(binDir, "ollama")
	temp := target + ".tmp"
	if err = os.WriteFile(temp, binary, 0755); err != nil {
		return err
	}
	defer os.Remove(temp)
	if err = os.Rename(temp, target); err != nil {
		return err
	}
	if err = linkManagedOllama(home, versionDir); err != nil {
		return err
	}
	fmt.Println("Installed verified Ollama", ollamaVersion, "at", target)
	return nil
}

func downloadVerifiedFile(ctx context.Context, artifact ollamaArtifact) (string, error) {
	return downloadVerifiedFileFrom(ctx, artifact, "https://github.com/ollama/ollama/releases/download", &http.Client{Timeout: 30 * time.Minute})
}

func downloadVerifiedFileFrom(ctx context.Context, artifact ollamaArtifact, releaseBaseURL string, client *http.Client) (string, error) {
	target := fmt.Sprintf("%s/v%s/%s", strings.TrimRight(releaseBaseURL, "/"), ollamaVersion, artifact.name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Ollama %s download returned %s", ollamaVersion, resp.Status)
	}
	file, err := os.CreateTemp("", "modeluplink-ollama-archive-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(name)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, artifact.maxBytes+1))
	if err != nil {
		return "", err
	}
	if written > artifact.maxBytes {
		return "", errors.New("Ollama artifact exceeds expected size")
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	if digest != artifact.sha256 {
		return "", fmt.Errorf("Ollama artifact checksum mismatch: got %s", digest)
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	keep = true
	return name, nil
}

func ChooseModel(reader io.Reader, writer io.Writer) (string, error) {
	return ChooseModelForHardware(reader, writer, Hardware{})
}

func ChooseModelForHardware(reader io.Reader, writer io.Writer, hardware Hardware) (string, error) {
	options := []string{"llama3.2:3b", "qwen2.5:3b", "gemma3:1b"}
	if hardware.RAMBytes >= 16<<30 {
		options = []string{"qwen2.5:7b", "llama3.1:8b", "gemma3:4b"}
	}
	if hardware.RAMBytes >= 32<<30 {
		options = []string{"qwen2.5:14b", "llama3.1:8b", "mistral-small:24b"}
	}
	fmt.Fprintln(writer, "No local models found. Choose a starter model:")
	for i, m := range options {
		fmt.Fprintf(writer, "  %d. %s\n", i+1, m)
	}
	fmt.Fprint(writer, "Model [1]: ")
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" || line == "1" {
		return options[0], nil
	}
	if line == "2" {
		return options[1], nil
	}
	if line == "3" {
		return options[2], nil
	}
	return line, nil
}
