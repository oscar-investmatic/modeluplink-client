package main

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/godbus/dbus/v5"
	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
)

// This window deliberately does not initialize the account UI or its helper:
// those binaries belong to the new package, while the old session still owns
// sharing. Only the launcher starts them after the old session has exited.
func runUpdateWindow() bool {
	a := app.NewWithID(appID + ".update")
	a.SetIcon(appIcon)
	a.Settings().SetTheme(uplinkTheme{theme.DefaultTheme()})
	w := a.NewWindow("Finish updating Model Uplink")
	w.SetIcon(appIcon)
	accepted := false
	updating := false
	w.SetCloseIntercept(func() {
		if !updating {
			w.Close()
		}
	})
	intro := widget.NewLabel("Close the existing Model Uplink window, then restart to use the update. Sharing will briefly disconnect and resume. Connections you stopped will stay stopped.")
	intro.Wrapping = fyne.TextWrapWord
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	cancel := widget.NewButton("Later", w.Close)
	var restart *widget.Button
	restart = widget.NewButton("Restart to update", func() {
		updating = true
		restart.Disable()
		cancel.Disable()
		status.SetText("Preparing to restart…")
		go func() {
			err := flatpak.PrepareUpdate()
			fyne.Do(func() {
				updating = false
				if err == nil {
					accepted = true
					w.Close()
					return
				}
				status.SetText(updateRecoveryMessage(err))
				restart.Enable()
				cancel.Enable()
			})
		}()
	})
	restart.Importance = widget.HighImportance
	w.SetContent(container.NewPadded(container.NewVBox(intro, status, container.NewGridWithColumns(2, cancel, restart))))
	w.Resize(fyne.NewSize(460, 240))
	w.ShowAndRun()
	return accepted
}

func updateRecoveryMessage(err error) string {
	var remote dbus.Error
	if !errors.As(err, &remote) {
		var pointer *dbus.Error
		if errors.As(err, &pointer) {
			remote = *pointer
		}
	}
	if remote.Name == "org.freedesktop.DBus.Error.UnknownMethod" {
		return "This older preview cannot restart automatically. Stop sharing and close its window, then reopen Model Uplink. If that window is already closed, sign out of Linux and sign back in first."
	}
	return "Close the existing Model Uplink window and let its current operation finish, then try again. You can choose Later to keep using the running version."
}
