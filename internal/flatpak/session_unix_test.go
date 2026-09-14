//go:build !windows

package flatpak

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
)

func testSession(t *testing.T) *session {
	t.Helper()
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := &session{agents: make(map[string]*worker), shutdown: make(chan struct{}), permission: func(bool) error { return nil }, command: func(string) *exec.Cmd { return exec.Command("sleep", "60") }}
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

func TestUpdateWaitsForWindowAndPendingWork(t *testing.T) {
	s := testSession(t)
	s.gui = exec.Command("sleep", "1")
	if err := s.PrepareUpdate("next-build"); err == nil || s.closing {
		t.Fatal("update interrupted an open window")
	}
	s.gui = nil
	s.pending = 1
	if err := s.PrepareUpdate("next-build"); err == nil || s.closing {
		t.Fatal("update interrupted a pending operation")
	}
	s.pending = 0
}

func TestUpdateStopsWorkersWithoutChangingSavedIntent(t *testing.T) {
	s := testSession(t)
	saveTestEndpoint(t, false)
	if err := s.start("lab-test"); err != nil {
		t.Fatal(err)
	}
	w := s.agents["lab-test"]
	if err := s.FinishUpdate("next-build"); err == nil {
		t.Fatal("unprepared shutdown accepted")
	}
	if err := s.PrepareUpdate("next-build"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.shutdown:
		t.Fatal("shutdown began before preparation could be acknowledged")
	default:
	}
	if err := s.Start("lab-test"); err == nil {
		t.Fatal("update allowed a competing start")
	}
	if err := s.FinishUpdate("next-build"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.shutdown:
	default:
		t.Fatal("update did not request shutdown")
	}
	s.close()
	select {
	case <-w.done:
	default:
		t.Fatal("update returned before the old worker stopped")
	}
	if _, _, err := savedEndpoint("lab-test"); err != nil {
		t.Fatalf("update changed saved sharing intent: %v", err)
	}
}

func TestUpdateRequiredRecognizesPreviewAndCurrentErrors(t *testing.T) {
	for _, err := range []error{
		dbus.NewError(ID+".Error.UpdateRequired", []interface{}{updateRequiredMessage}),
		dbus.MakeFailedError(errors.New(updateRequiredMessage)),
	} {
		if !updateRequired(err) {
			t.Fatal("update error was not recognized")
		}
	}
	if updateRequired(errors.New(updateRequiredMessage)) || updateRequired(dbus.MakeFailedError(errors.New("unrelated"))) {
		t.Fatal("unrelated failure triggered update")
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

func TestPermissionDenialIsNotReportedAsCancelled(t *testing.T) {
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_2/uplink_test")
	respond := func(code uint32, values map[string]dbus.Variant) error {
		t.Helper()
		handled, err := permissionResponse(&dbus.Signal{Name: "org.freedesktop.portal.Request.Response", Path: path, Body: []interface{}{code, values}}, path, false)
		if !handled {
			t.Fatal("permission response ignored")
		}
		return err
	}
	// xdg-desktop-portal answers a stored "no" and a declined dialog with
	// response 1 and background=false; KDE has also been seen returning 2.
	denied := map[string]dbus.Variant{"background": dbus.MakeVariant(false), "autostart": dbus.MakeVariant(false)}
	for _, code := range []uint32{0, 1, 2} {
		if err := respond(code, denied); !errors.Is(err, errBackgroundDenied) {
			t.Fatalf("code %d: denial reported as %v", code, err)
		}
	}
	if err := respond(1, map[string]dbus.Variant{}); err == nil || errors.Is(err, errBackgroundDenied) || !strings.Contains(err.Error(), "closed before it finished") {
		t.Fatalf("dismissed request reported as %v", err)
	}
	if err := respond(0, map[string]dbus.Variant{"background": dbus.MakeVariant(true), "autostart": dbus.MakeVariant(false)}); err != nil {
		t.Fatalf("granted permission rejected: %v", err)
	}
	if strings.Contains(errBackgroundDenied.Error(), "cancel") {
		t.Fatalf("denial wording still says cancelled: %q", errBackgroundDenied)
	}
}
