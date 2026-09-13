package localconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
)

type Endpoint struct {
	RuntimeOwnership     string    `json:"runtime_ownership,omitempty"`
	UpstreamKeyRef       string    `json:"upstream_key_ref,omitempty"`
	Revoked              bool      `json:"revoked,omitempty"`
	ID                   string    `json:"id"`
	Slug                 string    `json:"slug"`
	Engine               string    `json:"engine"`
	Region               string    `json:"region"`
	URL                  string    `json:"url"`
	RelayURL             string    `json:"relay_url"`
	ControlURL           string    `json:"control_url"`
	AgentToken           string    `json:"agent_token"`
	UpstreamURL          string    `json:"upstream_url"`
	UpstreamKey          string    `json:"upstream_key,omitempty"`
	AllowLAN             bool      `json:"allow_lan"`
	CORSOrigins          []string  `json:"cors_origins,omitempty"`
	Stopped              bool      `json:"stopped,omitempty"`
	MemoryReleasePending bool      `json:"memory_release_pending,omitempty"`
	SharedModels         []string  `json:"shared_models,omitempty"`
	Trial                bool      `json:"trial,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

type Config struct {
	UpgradeResume   []string            `json:"upgrade_resume,omitempty"`
	StartupDisabled bool                `json:"startup_disabled,omitempty"`
	ControlURL      string              `json:"control_url"`
	AccountToken    string              `json:"account_token"`
	Endpoints       map[string]Endpoint `json:"endpoints"`
}

func Path() (string, error) {
	// Allows isolated native-app previews and alternate local profiles without
	// changing HOME or mixing test credentials with a real installation.
	if directory := os.Getenv("MODELUPLINK_CONFIG_DIR"); directory != "" {
		if !filepath.IsAbs(directory) {
			return "", errors.New("MODELUPLINK_CONFIG_DIR must be absolute")
		}
		return filepath.Join(directory, "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return "", errors.New("LOCALAPPDATA is not configured")
		}
		return filepath.Join(base, "Model Uplink", "config.json"), nil
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Model Uplink", "config.json"), nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "modeluplink", "config.json"), nil
}
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{ControlURL: "https://api.modeluplink.com", Endpoints: map[string]Endpoint{}, StartupDisabled: os.Getenv("FLATPAK_ID") == "com.modeluplink.app"}, nil
	}
	if err != nil {
		return Config{}, err
	}
	data, err = unprotectPayload(data)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err = json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Endpoints == nil {
		cfg.Endpoints = map[string]Endpoint{}
	}
	return cfg, nil
}
func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err = PrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	safe := cfg
	safe.Endpoints = make(map[string]Endpoint, len(cfg.Endpoints))
	for k, e := range cfg.Endpoints {
		e, err = protectUpstream(e)
		if err != nil {
			return err
		}
		safe.Endpoints[k] = e
	}
	payload, err := json.MarshalIndent(safe, "", "  ")
	if err != nil {
		return err
	}
	payload, err = protectPayload(payload)
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err = os.WriteFile(temp, payload, 0600); err != nil {
		return err
	}
	if err = os.Chmod(temp, 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
func EndpointPath(slug string) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "endpoints", slug+".json"), nil
}
func SaveEndpoint(e Endpoint) (string, error) {
	path, err := EndpointPath(e.Slug)
	if err != nil {
		return "", err
	}
	if err = PrivateDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	e, err = protectUpstream(e)
	if err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return "", err
	}
	payload, err = protectPayload(payload)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(path+".tmp", payload, 0600); err != nil {
		return "", err
	}
	if err = os.Chmod(path+".tmp", 0600); err != nil {
		return "", err
	}
	if err = os.Rename(path+".tmp", path); err != nil {
		return "", err
	}
	return path, nil
}
func LoadEndpoint(path string) (Endpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Endpoint{}, err
	}
	data, err = unprotectPayload(data)
	if err != nil {
		return Endpoint{}, err
	}
	var e Endpoint
	err = json.Unmarshal(data, &e)
	return e, err
}

func DeleteEndpointFiles(slug string) error {
	if err := security.ValidateSlug(slug); err != nil {
		return err
	}
	path, err := EndpointPath(slug)
	if err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	certificateDir := filepath.Join(filepath.Dir(path), slug+"-certificates")
	return os.RemoveAll(certificateDir)
}

// ProtectSecrets protects a serialized credential file with the platform's user protection.
func ProtectSecrets(data []byte) ([]byte, error) { return protectPayload(data) }

// UnprotectSecrets reads a serialized credential file protected for the current user.
func UnprotectSecrets(data []byte) ([]byte, error) { return unprotectPayload(data) }

// Missing ownership preserves legacy managed Ollama behavior.
func (e Endpoint) ManagesRuntime() bool {
	return e.Engine == "ollama" && e.RuntimeOwnership != "external"
}
