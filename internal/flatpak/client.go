// Package flatpak provides the session lifecycle used by the sandboxed desktop.
package flatpak

import (
	"context"
	"errors"
	"os"
	"runtime"
	"time"

	"github.com/godbus/dbus/v5"
)

const ID = "com.modeluplink.app"
const objectPath dbus.ObjectPath = "/com/modeluplink/app/Session"
const sessionInterface = ID + ".Session"

// UpdateAcceptedExit is the private result of the standalone update window.
const UpdateAcceptedExit = 23

// BuildID identifies the packaged binaries, including development candidates.
var BuildID = "dev"

// UserError carries a bounded, authored permission message through service wrappers.
type UserError struct{ Message string }

func (e *UserError) Error() string { return e.Message }

func Enabled() bool { return runtime.GOOS == "linux" && os.Getenv("FLATPAK_ID") == ID }

func PrepareUpdate() error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	return prepareUpdate(ctx, conn)
}

func prepareUpdate(ctx context.Context, conn *dbus.Conn) error {
	owner := func() (string, error) {
		var name string
		err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, ID).Store(&name)
		return name, err
	}
	previous, err := owner()
	if noSessionOwner(err) {
		// A stopped session can exit when its last window closes.
		return nil
	}
	if err != nil {
		return err
	}
	object := conn.Object(ID, objectPath)
	if err = object.CallWithContext(ctx, sessionInterface+".PrepareUpdate", 0, BuildID).Err; err != nil {
		if noSessionOwner(err) {
			return nil
		}
		return err
	}
	// Keep the connection open and wait for the old owner to disappear. A
	// fire-and-forget call followed by Close can be dropped by the sandbox's
	// D-Bus proxy. The old process may exit before replying, so its bus-name
	// release, rather than the FinishUpdate reply, establishes completion.
	finishErr := object.CallWithContext(ctx, sessionInterface+".FinishUpdate", 0, BuildID).Err
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		current, err := owner()
		if noSessionOwner(err) || (err == nil && current != previous) {
			return nil
		}
		if err != nil {
			return err
		}
		if finishErr != nil {
			return finishErr
		}
		select {
		case <-ctx.Done():
			return errors.New("The previous app has not finished closing. Try the update again.")
		case <-tick.C:
		}
	}
}

func noSessionOwner(err error) bool {
	var remote dbus.Error
	return errors.As(err, &remote) && (remote.Name == "org.freedesktop.DBus.Error.ServiceUnknown" || remote.Name == "org.freedesktop.DBus.Error.NameHasNoOwner")
}

func Call(method string, args ...interface{}) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return errors.New("The desktop session is unavailable. Sign in again and reopen Model Uplink.")
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	if err = conn.Object(ID, objectPath).CallWithContext(ctx, sessionInterface+"."+method, 0, args...).Err; err != nil {
		var remote dbus.Error
		if errors.As(err, &remote) && remote.Name == ID+".Error.Permission" && len(remote.Body) == 1 {
			if message, ok := remote.Body[0].(string); ok && len(message) < 512 {
				return &UserError{Message: message}
			}
		}
		return err
	}
	return nil
}
