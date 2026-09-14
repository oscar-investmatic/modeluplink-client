//go:build !windows

package flatpak

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// Subscribe before requesting permission: portals may reply before the method
// call returns. Use a private connection so concurrent requests cannot race.
func requestBackground(automatic bool) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	tokenBytes := make([]byte, 12)
	if _, err = rand.Read(tokenBytes); err != nil {
		return err
	}
	token := "uplink_" + hex.EncodeToString(tokenBytes)
	sender := strings.ReplaceAll(strings.TrimPrefix(conn.Names()[0], ":"), ".", "_")
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	if err = conn.AddMatchSignal(dbus.WithMatchObjectPath(path), dbus.WithMatchInterface("org.freedesktop.portal.Request"), dbus.WithMatchMember("Response")); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	options := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token),
		"reason":       dbus.MakeVariant("Keep your shared model available after closing the window."),
		"autostart":    dbus.MakeVariant(automatic),
		"commandline":  dbus.MakeVariant([]string{"modeluplink", "_flatpak", "--background"}),
	}
	var handle dbus.ObjectPath
	err = conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop").CallWithContext(ctx, "org.freedesktop.portal.Background.RequestBackground", 0, "", options).Store(&handle)
	if err != nil {
		return errors.New("Background permission is unavailable. Check your desktop portal and try again.")
	}
	defer conn.Object("org.freedesktop.portal.Desktop", handle).Call("org.freedesktop.portal.Request.Close", 0)
	for {
		select {
		case signal := <-signals:
			if handled, err := permissionResponse(signal, path, automatic); handled {
				return err
			}
		case <-ctx.Done():
			return errors.New("Background permission timed out. Reopen the app and try again.")
		}
	}
}

func permissionResponse(signal *dbus.Signal, path dbus.ObjectPath, automatic bool) (bool, error) {
	if signal == nil {
		return true, errors.New("The desktop permission connection closed. Try again.")
	}
	// D-Bus also delivers NameAcquired/NameLost independently of match rules.
	if signal.Path != path || signal.Name != "org.freedesktop.portal.Request.Response" {
		return false, nil
	}
	if len(signal.Body) != 2 {
		return true, errors.New("The desktop returned an invalid background permission response.")
	}
	code, ok := signal.Body[0].(uint32)
	values, valid := signal.Body[1].(map[string]dbus.Variant)
	if !ok || !valid {
		return true, errors.New("The desktop returned an invalid background permission response.")
	}
	// A stored "no" and a declined dialog both arrive as a cancelled response
	// carrying background=false, so read the result before the response code.
	if result, present := values["background"]; present {
		if allowed, _ := result.Value().(bool); !allowed {
			return true, errBackgroundDenied
		}
	}
	if code != 0 {
		return true, errors.New("The background permission request closed before it finished. Start sharing again and choose Allow when your desktop asks.")
	}
	return true, checkPermission(values, automatic)
}

var errBackgroundDenied = errors.New("Model Uplink isn’t allowed to run in the background. Choose Allow if your desktop asks, or turn on background activity for Model Uplink in your desktop’s app settings, then start sharing again.")

func checkPermission(values map[string]dbus.Variant, automatic bool) error {
	allowed, _ := values["background"].Value().(bool)
	auto, _ := values["autostart"].Value().(bool)
	if !allowed {
		return errBackgroundDenied
	}
	if auto != automatic {
		return errors.New("The desktop did not apply the requested automatic-start setting. Review its application permissions and try again.")
	}
	return nil
}
