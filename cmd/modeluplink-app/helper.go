package main

// The app is a thin client of the CLI's internal `_desktop` command. Each
// action spawns the helper, writes one JSON request to its stdin, and reads
// newline-delimited JSON from stdout: zero or more progress events followed by
// exactly one final response. stderr is discarded. Credentials never enter
// command-line arguments, a local listener, or logs.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oscar-investmatic/modeluplink-client/internal/hostexec"
)

const (
	helperEnv      = "MODELUPLINK_HELPER"
	helperBaseName = "modeluplink"
	helperTimeout  = 90 * time.Second
	connectTimeout = 30 * time.Minute
	maxHelperLine  = 1 << 20
)

var (
	helperName = func() string {
		if runtime.GOOS == "windows" {
			return helperBaseName + ".exe"
		}
		return helperBaseName
	}()
	errHelperMissing = errors.New("The connection helper couldn’t be found. Reinstall Model Uplink, then reopen the app.")
	errHelper        = errors.New("The connection helper couldn’t start. Try reopening Model Uplink.")
	errHelperTimeout = errors.New("That took too long. Check your internet connection and try again.")
)

// account and endpoint carry the fields the interface shows. Extra fields in
// the helper's JSON are ignored so the app keeps working as the helper grows.
type account struct {
	Email                  string `json:"email"`
	BillingState           string `json:"billing_state"`
	TrialStatus            string `json:"trial_status"`
	TrialRemaining         int    `json:"trial_remaining"`
	TrialTransferRemaining *int64 `json:"trial_transfer_remaining"`
}

type endpoint struct {
	ID     string `json:"id"`
	Slug   string `json:"slug"`
	URL    string `json:"url"`
	Online bool   `json:"online"`
}

type modelActivity struct {
	Model   string `json:"model"`
	Running int    `json:"running"`
	Waiting int    `json:"waiting"`
}

type helperReply struct {
	Source               *localServer             `json:"source,omitempty"`
	Servers              []localServer            `json:"servers,omitempty"`
	LocalSources         map[string]localServer   `json:"local_sources,omitempty"`
	StartupEnabled       *bool                    `json:"startup_enabled,omitempty"`
	Retired              []endpoint               `json:"retired"`
	ConnectionLimit      *endpoint                `json:"connection_limit"`
	OtherTrial           *endpoint                `json:"other_trial"`
	SharedModels         map[string][]string      `json:"shared_models"`
	Activity             map[string]modelActivity `json:"activity"`
	Stopped              map[string]bool          `json:"stopped"`
	MemoryReleasePending map[string]bool          `json:"memory_release_pending"`
	Unavailable          bool                     `json:"unavailable"`
	Error                string                   `json:"error"`
	Notice               string                   `json:"notice"`
	Challenge            string                   `json:"challenge_id"`
	Account              *account                 `json:"account"`
	Endpoints            []endpoint               `json:"endpoints"`
	Models               []string                 `json:"models"`
	Recommendation       string                   `json:"recommendation"`
	NameSuggestions      []string                 `json:"name_suggestions"`
	Endpoint             *endpoint                `json:"endpoint"`
	APIKey               string                   `json:"api_key"`
	Ready                bool                     `json:"ready"`
}

type progressEvent struct {
	Event     string `json:"event"`
	Stage     string `json:"stage"`
	Message   string `json:"message"`
	Completed int64  `json:"completed"`
	Total     int64  `json:"total"`
}

// fraction is the current file's completion, or -1 when the size is unknown.
func (p progressEvent) fraction() float64 {
	if p.Total <= 0 {
		return -1
	}
	return min(1, max(0, float64(p.Completed)/float64(p.Total)))
}

// helperRunner performs one helper request. Tests substitute a scripted fake.
type helperRunner interface {
	run(ctx context.Context, request map[string]string, onProgress func(progressEvent)) (helperReply, error)
}

// processHelper spawns the real helper binary. A successful lookup is cached;
// a failed one is retried so "Try again" works after the helper is installed.
type processHelper struct {
	mu   sync.Mutex
	path string
	// locate is replaced in tests; nil means findHelper.
	locate func() (string, error)
}

func (h *processHelper) resolve() (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.path != "" {
		return h.path, nil
	}
	locate := h.locate
	if locate == nil {
		locate = findHelper
	}
	path, err := locate()
	if err != nil {
		return "", err
	}
	h.path = path
	return path, nil
}

// findHelper looks for the CLI: an explicit override, a copy shipped next to
// the app, the user's local bin directory, then PATH.
func findHelper() (string, error) {
	if override := os.Getenv(helperEnv); override != "" {
		if usable(override) {
			return override, nil
		}
		return "", errHelperMissing
	}
	if executable, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		if candidate := filepath.Join(filepath.Dir(executable), helperName); usable(candidate) {
			return candidate, nil
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if candidate := filepath.Join(home, ".local", "bin", helperName); usable(candidate) {
			return candidate, nil
		}
	}
	if candidate, err := exec.LookPath(helperName); err == nil {
		return candidate, nil
	}
	return "", errHelperMissing
}

func usable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0o111 != 0)
}

// helperEnvironment passes the app's environment through so preview settings
// (MODELUPLINK_CONTROL_URL, MODELUPLINK_CONFIG_DIR) reach the helper, drops any
// stray account token, and widens PATH for the common Ollama install locations
// because desktop sessions do not always inherit a login shell's PATH.
func helperEnvironment() []string {
	var env []string
	path := ""
	for _, entry := range os.Environ() {
		switch {
		case strings.HasPrefix(strings.ToUpper(entry), "MODELUPLINK_ACCOUNT_TOKEN="):
			continue
		case strings.HasPrefix(strings.ToUpper(entry), "PATH="):
			path = entry[len("PATH="):]
			continue
		}
		env = append(env, entry)
	}
	extra := []string{"/usr/local/bin", "/usr/bin", "/bin", "/opt/homebrew/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		extra = append([]string{filepath.Join(home, ".local", "bin")}, extra...)
	}
	if runtime.GOOS == "windows" {
		extra = []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Ollama")}
	}
	separator := string(os.PathListSeparator)
	var parts []string
	for _, dir := range strings.Split(path, separator) {
		if dir != "" {
			parts = append(parts, dir)
		}
	}
	for _, dir := range extra {
		if !slices.Contains(parts, dir) {
			parts = append(parts, dir)
		}
	}
	return append(env, "PATH="+strings.Join(parts, separator))
}

func requestTimeout(action string) time.Duration {
	if action == "connect" || action == "move_trial" || action == "share_models" || action == "stop_all" {
		return connectTimeout
	}
	return helperTimeout
}

func (h *processHelper) run(parent context.Context, request map[string]string, onProgress func(progressEvent)) (helperReply, error) {
	path, err := h.resolve()
	if err != nil {
		return helperReply{}, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return helperReply{}, errHelper
	}
	ctx, cancel := context.WithTimeout(parent, requestTimeout(request["action"]))
	defer cancel()
	cmd := hostexec.CommandContext(ctx, path, "_desktop")
	cmd.Env = helperEnvironment()
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stderr = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return helperReply{}, errHelper
	}
	if err := cmd.Start(); err != nil {
		return helperReply{}, errHelper
	}
	reply, parseErr := parseHelperStream(stdout, onProgress)
	if parseErr != nil {
		_ = cmd.Process.Kill()
		_ = stdout.Close()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return helperReply{}, errHelperTimeout
	}
	if parseErr != nil {
		return helperReply{}, parseErr
	}
	if waitErr != nil {
		return helperReply{}, errHelper
	}
	return reply, nil
}

// parseHelperStream reads progress lines until the single final response.
// Anything after the response, an unknown event, or a missing response is a
// protocol failure rather than something to show the user.
func parseHelperStream(r io.Reader, onProgress func(progressEvent)) (helperReply, error) {
	scanner := bufio.NewScanner(r)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if end := bytes.IndexByte(data, '\n'); end >= 0 {
			return end + 1, data[:end], nil
		}
		if atEOF && len(data) > 0 {
			return 0, nil, io.ErrUnexpectedEOF
		}
		return 0, nil, nil
	})
	scanner.Buffer(make([]byte, 0, 64*1024), maxHelperLine)
	var reply *helperReply
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if reply != nil {
			return helperReply{}, errHelper
		}
		var header struct {
			Event *string `json:"event"`
		}
		if err := json.Unmarshal(line, &header); err != nil {
			return helperReply{}, errHelper
		}
		switch {
		case header.Event == nil:
			var final helperReply
			if err := json.Unmarshal(line, &final); err != nil {
				return helperReply{}, errHelper
			}
			reply = &final
		case *header.Event == "progress":
			var event progressEvent
			if err := json.Unmarshal(line, &event); err != nil {
				return helperReply{}, errHelper
			}
			if onProgress != nil {
				onProgress(event)
			}
		default:
			return helperReply{}, errHelper
		}
	}
	if scanner.Err() != nil || reply == nil {
		return helperReply{}, errHelper
	}
	return *reply, nil
}
