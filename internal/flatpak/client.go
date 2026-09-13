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

// BuildID identifies the packaged binaries, including development candidates.
var BuildID = "dev"

// UserError carries a bounded, authored permission message through service wrappers.
type UserError struct{ Message string }

func (e *UserError) Error() string { return e.Message }

func Enabled() bool { return runtime.GOOS == "linux" && os.Getenv("FLATPAK_ID") == ID }

func PrepareUpdate() error {
	if err := Call("PrepareUpdate", BuildID); err != nil {
		var remote dbus.Error
		if errors.As(err, &remote) && (remote.Name == "org.freedesktop.DBus.Error.ServiceUnknown" || remote.Name == "org.freedesktop.DBus.Error.NameHasNoOwner") {
			// Closing the old window can make an already-stopped session exit
			// before the user presses Restart. There is nothing left to stop.
			return nil
		}
		return err
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Object(ID, objectPath).Call(sessionInterface+".FinishUpdate", dbus.FlagNoReplyExpected, BuildID).Err
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
