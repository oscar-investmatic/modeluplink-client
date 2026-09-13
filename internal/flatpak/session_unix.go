//go:build !windows

package flatpak

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
)

const updateRequiredMessage = "An older Model Uplink is still running. Stop sharing and close it before opening the updated package."

func updateRequired(err error) bool {
	var remote dbus.Error
	if !errors.As(err, &remote) {
		var pointer *dbus.Error
		if !errors.As(err, &pointer) {
			return false
		}
		remote = *pointer
	}
	if remote.Name == ID+".Error.UpdateRequired" {
		return true
	}
	// Early preview packages used the generic D-Bus error name.
	return remote.Name == "org.freedesktop.DBus.Error.Failed" && len(remote.Body) == 1 && remote.Body[0] == updateRequiredMessage
}

type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type session struct {
	mu         sync.Mutex
	agents     map[string]*worker
	gui        *exec.Cmd
	guiError   error
	executable string
	permission func(bool) error
	command    func(string) *exec.Cmd
	pending    int
	closing    bool
	shutdown   chan struct{}
}

func savedEndpoint(slug string) (localconfig.Endpoint, string, error) {
	if err := security.ValidateSlug(slug); err != nil {
		return localconfig.Endpoint{}, "", err
	}
	path, err := localconfig.EndpointPath(slug)
	if err != nil {
		return localconfig.Endpoint{}, "", err
	}
	e, err := localconfig.LoadEndpoint(path)
	if err != nil {
		return e, path, err
	}
	if e.Slug != slug || e.Stopped || e.Revoked {
		return e, path, errors.New("This connection is stopped or no longer available.")
	}
	if e.ManagesRuntime() {
		return e, path, errors.New("Use a separately installed model server with the Flatpak edition.")
	}
	return e, path, nil
}

func (s *session) start(slug string) error {
	_, path, err := savedEndpoint(slug)
	if err != nil {
		return err
	}
	// Hold this lock through replacement; concurrent starts/stops cannot create
	// two agents for one connection or return before the old one has exited.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return errors.New("Model Uplink is restarting to finish an update.")
	}
	if old := s.agents[slug]; old != nil {
		old.cancel()
		<-old.done
		delete(s.agents, slug)
	}
	logPath, err := LogPath(slug)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return err
	}
	if info, statErr := os.Stat(logPath); statErr == nil && info.Size() > 5<<20 {
		_ = os.Rename(logPath, logPath+".previous")
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	cmd := s.command(path)
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		log.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &worker{cancel: cancel, done: make(chan struct{})}
	s.agents[slug] = w
	go func() {
		defer close(w.done)
		defer log.Close()
		for {
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			select {
			case <-ctx.Done():
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-exited:
				case <-time.After(12 * time.Second):
					_ = cmd.Process.Kill()
					<-exited
				}
				return
			case <-exited:
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			// Revocation and explicit Stop are authoritative across every restart.
			if _, _, err := savedEndpoint(slug); err != nil {
				return
			}
			cmd = s.command(path)
			cmd.Stdout, cmd.Stderr = log, log
			if err = cmd.Start(); err != nil {
				fmt.Fprintln(log, "Could not restart connection agent:", err)
				return
			}
		}
	}()
	return nil
}

func (s *session) Start(slug string) *dbus.Error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return dbus.MakeFailedError(errors.New("Model Uplink is restarting to finish an update."))
	}
	s.pending++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.pending--; s.mu.Unlock() }()
	cfg, err := localconfig.Load()
	if err == nil {
		err = s.permission(!cfg.StartupDisabled)
		if err != nil {
			return dbus.NewError(ID+".Error.Permission", []interface{}{err.Error()})
		}
	}
	if err == nil {
		err = s.start(slug)
	}
	if err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}
func (s *session) Stop(slug string) *dbus.Error {
	if err := security.ValidateSlug(slug); err != nil {
		return dbus.MakeFailedError(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.agents[slug]; w != nil {
		w.cancel()
		<-w.done
		delete(s.agents, slug)
	}
	return nil
}
func (s *session) Startup(enabled bool) *dbus.Error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return dbus.MakeFailedError(errors.New("Model Uplink is restarting to finish an update."))
	}
	s.pending++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.pending--; s.mu.Unlock() }()
	if err := s.permission(enabled); err != nil {
		return dbus.NewError(ID+".Error.Permission", []interface{}{err.Error()})
	}
	return nil
}

// The caller is the retiring agent itself; never wait for that caller to exit.
func (s *session) Retire(slug string) *dbus.Error {
	if err := security.ValidateSlug(slug); err != nil {
		return dbus.MakeFailedError(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.agents[slug]; w != nil {
		w.cancel()
	}
	return nil
}
func (s *session) Open(build string) *dbus.Error {
	if build != BuildID {
		return dbus.NewError(ID+".Error.UpdateRequired", []interface{}{updateRequiredMessage})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return dbus.MakeFailedError(errors.New("Model Uplink is restarting to finish an update."))
	}
	if s.gui != nil {
		return nil
	}
	cmd := exec.Command(filepath.Join(filepath.Dir(s.executable), "modeluplink-app"))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return dbus.MakeFailedError(err)
	}
	s.gui = cmd
	s.guiError = nil
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.gui = nil
		s.guiError = err
		s.mu.Unlock()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Desktop window exited:", err)
		}
	}()
	return nil
}

// PrepareUpdate releases this sandbox session after the update window obtains
// consent. Saved Stop settings are preserved; the new session resumes only
// connections that were already sharing.
func (s *session) PrepareUpdate(build string) *dbus.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if build == BuildID || s.pending != 0 || s.gui != nil {
		return dbus.MakeFailedError(errors.New("Close the existing app window and let its current operation finish, then try again."))
	}
	if !s.closing {
		s.closing = true
	}
	return nil
}

// FinishUpdate begins shutdown after PrepareUpdate acknowledges the request.
// The caller observes bus-name release because process exit can race the reply.
func (s *session) FinishUpdate(build string) *dbus.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closing || build == BuildID {
		return dbus.MakeFailedError(errors.New("Prepare the update before finishing it."))
	}
	select {
	case <-s.shutdown:
	default:
		close(s.shutdown)
	}
	return nil
}
func (s *session) idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	for slug, w := range s.agents {
		select {
		case <-w.done:
			delete(s.agents, slug)
		default:
		}
	}
	return s.gui == nil && len(s.agents) == 0 && s.pending == 0
}
func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.agents {
		w.cancel()
	}
	for _, w := range s.agents {
		<-w.done
	}
	if s.gui != nil {
		_ = s.gui.Process.Signal(syscall.SIGTERM)
	}
}

func LogPath(slug string) (string, error) {
	if err := security.ValidateSlug(slug); err != nil {
		return "", err
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "modeluplink", "logs", slug+".log"), nil
}

func Run(backgroundOnly bool) error {
	if !Enabled() {
		return errors.New("This launcher is only available inside the Model Uplink Flatpak.")
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	reply, err := conn.RequestName(ID, dbus.NameFlagDoNotQueue)
	if err != nil {
		return err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		if backgroundOnly {
			return nil
		}
		err = Call("Open", BuildID)
		if !updateRequired(err) {
			return err
		}
		executable, pathErr := os.Executable()
		if pathErr != nil {
			return pathErr
		}
		prompt := exec.Command(filepath.Join(filepath.Dir(executable), "modeluplink-app"), "--flatpak-update")
		prompt.Stdout, prompt.Stderr = os.Stdout, os.Stderr
		var result *exec.ExitError
		if promptErr := prompt.Run(); !errors.As(promptErr, &result) || result.ExitCode() != UpdateAcceptedExit {
			return promptErr
		}
		// The old session closes agents before releasing its bus name. Never
		// start a replacement while it can still serve the same connection.
		deadline := time.Now().Add(30 * time.Second)
		for {
			reply, err = conn.RequestName(ID, dbus.NameFlagDoNotQueue)
			if err != nil {
				return err
			}
			if reply == dbus.RequestNameReplyPrimaryOwner {
				break
			}
			if time.Now().After(deadline) {
				return errors.New("The previous app is still closing. Open Model Uplink again in a moment.")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	s := &session{agents: make(map[string]*worker), shutdown: make(chan struct{}), executable: executable, permission: requestBackground,
		command: func(path string) *exec.Cmd { return exec.Command(executable, "_agent", "--config", path) }}
	defer s.close()
	if err = conn.Export(s, objectPath, sessionInterface); err != nil {
		return err
	}
	cfg, err := localconfig.Load()
	if err != nil {
		return err
	}
	if backgroundOnly && cfg.StartupDisabled {
		return nil
	}
	if !backgroundOnly {
		if e := s.Open(BuildID); e != nil {
			return e
		}
	}
	// Register pending work before starting the goroutine to prevent idle exit.
	s.mu.Lock()
	s.pending++
	s.mu.Unlock()
	go func() {
		defer func() { s.mu.Lock(); s.pending--; s.mu.Unlock() }()
		for slug := range cfg.Endpoints {
			if _, _, err := savedEndpoint(slug); err == nil {
				if err := s.Start(slug); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			}
		}
	}()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.shutdown:
			return nil
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if s.idle() {
				s.mu.Lock()
				err := s.guiError
				s.mu.Unlock()
				return err
			}
		}
	}
}
