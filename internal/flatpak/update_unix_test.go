//go:build !windows

package flatpak

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type updateFixture struct {
	conn    *dbus.Conn
	started chan struct{}
	release chan struct{}
}

func (*updateFixture) PrepareUpdate(string) *dbus.Error { return nil }

func (f *updateFixture) FinishUpdate(string) *dbus.Error {
	close(f.started)
	<-f.release
	// Deliberately drop the method reply, as the real session can do when
	// shutdown closes its bus connection before the reply is sent.
	_ = f.conn.Close()
	return nil
}

func TestUpdateWaitsForOldSessionDespiteDroppedReply(t *testing.T) {
	path, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("private D-Bus integration test requires dbus-daemon")
	}
	cmd := exec.Command(path, "--session", "--nofork", "--print-address=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	server, err := dbus.Connect(strings.TrimSpace(address))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	fixture := &updateFixture{conn: server, started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(fixture.release) }) })
	if err = server.Export(fixture, objectPath, sessionInterface); err != nil {
		t.Fatal(err)
	}
	if _, err = server.RequestName(ID, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	client, err := dbus.Connect(strings.TrimSpace(address))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- prepareUpdate(ctx, client) }()
	select {
	case <-fixture.started:
	case err := <-result:
		t.Fatalf("update returned before shutdown was received: %v", err)
	case <-ctx.Done():
		t.Fatal("shutdown request did not arrive")
	}
	select {
	case err := <-result:
		t.Fatalf("update returned while the old session was still running: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release.Do(func() { close(fixture.release) })
	if err = <-result; err != nil {
		t.Fatalf("dropped reply obscured successful shutdown: %v", err)
	}
}
