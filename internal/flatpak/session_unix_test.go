//go:build !windows

package flatpak

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
)

func testSession(t *testing.T) *session {
	t.Helper()
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := &session{agents: make(map[string]*worker), permission: func(bool) error { return nil }, command: func(string) *exec.Cmd { return exec.Command("sleep", "60") }}
	t.Cleanup(s.close)
	return s
}
func saveTestEndpoint(t *testing.T, stopped bool) {
	t.Helper()
	_, err := localconfig.SaveEndpoint(localconfig.Endpoint{ID: "test-id", Slug: "lab-test", RuntimeOwnership: "external", Stopped: stopped})
	if err != nil {
		t.Fatal(err)
	}
}
func TestDeniedPermissionNeverStartsAgent(t *testing.T) {
	s := testSession(t)
	saveTestEndpoint(t, false)
	s.permission = func(bool) error { return errors.New("denied") }
	if err := s.Start("lab-test"); err == nil {
		t.Fatal("permission denial ignored")
	}
	if !s.idle() {
		t.Fatal("denial left background work running")
	}
}
func TestRestartReplacesAgentAndStopWaitsForExit(t *testing.T) {
	s := testSession(t)
	saveTestEndpoint(t, false)
	if err := s.start("lab-test"); err != nil {
		t.Fatal(err)
	}
	first := s.agents["lab-test"]
	if err := s.start("lab-test"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.done:
	default:
		t.Fatal("duplicate start left old agent running")
	}
	current := s.agents["lab-test"]
	if err := s.Stop("lab-test"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-current.done:
	default:
		t.Fatal("Stop returned before exit")
	}
	if !s.idle() {
		t.Fatal("Stop left background work")
	}
}
func TestStoppedAndRevokedEndpointsCannotResume(t *testing.T) {
	s := testSession(t)
	saveTestEndpoint(t, true)
	if err := s.start("lab-test"); err == nil {
		t.Fatal("restarted a stopped endpoint")
	}
	_, err := localconfig.SaveEndpoint(localconfig.Endpoint{ID: "test-id", Slug: "lab-test", RuntimeOwnership: "external", Revoked: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.start("lab-test"); err == nil {
		t.Fatal("restarted revoked endpoint")
	}
	if err := s.start("../another"); err == nil {
		t.Fatal("accepted path traversal")
	}
}
func TestAutostartDoesNotChangeCurrentAgent(t *testing.T) {
	s := testSession(t)
	saveTestEndpoint(t, false)
	if err := s.start("lab-test"); err != nil {
		t.Fatal(err)
	}
	current := s.agents["lab-test"]
	if err := s.Startup(false); err != nil {
		t.Fatal(err)
	}
	if current != s.agents["lab-test"] {
		t.Fatal("startup setting replaced running agent")
	}
	select {
	case <-current.done:
		t.Fatal("startup setting stopped sharing")
	default:
	}
}
func TestRetiringAgentDoesNotWaitForItsOwnExit(t *testing.T) {
	s := testSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	w := &worker{cancel: cancel, done: make(chan struct{})}
	s.agents["lab-test"] = w
	// Simulate the agent waiting for its retirement reply before it can exit.
	returned := make(chan struct{})
	go func() { _ = s.Retire("lab-test"); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		close(w.done)
		t.Fatal("retirement deadlocked with its caller")
	}
	if ctx.Err() == nil {
		t.Fatal("retirement did not cancel agent")
	}
	close(w.done)
}
func TestBackgroundPermissionResults(t *testing.T) {
	for _, c := range []struct{ bg, gotAuto, wantAuto, pass bool }{
		{false, false, false, false}, {true, false, true, false},
		{true, true, false, false}, {true, false, false, true}, {true, true, true, true},
	} {
		err := checkPermission(map[string]dbus.Variant{"background": dbus.MakeVariant(c.bg), "autostart": dbus.MakeVariant(c.gotAuto)}, c.wantAuto)
		if (err == nil) != c.pass {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	if err := checkPermission(nil, false); err == nil {
		t.Fatal("missing result allowed sharing")
	}
}

func TestPermissionIgnoresUnrelatedDBusSignals(t *testing.T) {
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_2/uplink_test")
	unrelated := &dbus.Signal{Name: "org.freedesktop.DBus.NameAcquired", Path: "/org/freedesktop/DBus", Body: []interface{}{":1.2"}}
	if handled, _ := permissionResponse(unrelated, path, false); handled {
		t.Fatal("unrelated signal consumed the permission response")
	}
	response := &dbus.Signal{Name: "org.freedesktop.portal.Request.Response", Path: path, Body: []interface{}{uint32(0), map[string]dbus.Variant{"background": dbus.MakeVariant(true), "autostart": dbus.MakeVariant(false)}}}
	if handled, err := permissionResponse(response, path, false); !handled || err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
}
