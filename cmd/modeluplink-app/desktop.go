package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/oscar-investmatic/modeluplink-client/internal/buildinfo"
	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
	"net/url"
	"runtime"
)

// Lifecycle controls are shared by Linux and Windows; the service adapters own
// their different sign-out and startup behavior.
func configureTray(a fyne.App, w fyne.Window, u *ui) bool {
	app, ok := a.(desktop.App)
	if !ok {
		return false
	}
	app.SetSystemTrayIcon(appIcon)
	menu := fyne.NewMenu("Model Uplink",
		fyne.NewMenuItem("Open", func() { fyne.Do(func() { w.Show(); w.RequestFocus() }) }),
		fyne.NewMenuItem("Stop sharing", func() { fyne.Do(func() { w.Show(); u.stopAll() }) }),
		fyne.NewMenuItem("Exit", func() {
			fyne.Do(func() {
				if u.working() {
					w.Show()
					dialog.ShowInformation("Finishing up", "Please let this step finish before exiting.", w)
					return
				}
				w.Show()
				u.confirm("Exit Model Uplink?", "Sharing continues in the background. Choose Stop sharing to disconnect remote access.", "Exit", func() { u.stop(); a.Quit() })
			})
		}),
	)
	// Prevent Fyne from appending an unguarded duplicate Quit action.
	menu.Items[len(menu.Items)-1].IsQuit = true
	app.SetSystemTrayMenu(menu)
	w.SetCloseIntercept(func() { w.Hide() })
	return true
}
func desktopSettings(u *ui) []fyne.CanvasObject {
	button := widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		problem := widget.NewLabel("")
		problem.Wrapping = fyne.TextWrapWord
		problem.Importance = widget.WarningImportance
		startup := u.startupControl(problem)
		update := widget.NewButton("Downloads / updates", func() { target, _ := url.Parse("https://modeluplink.com/download/"); _ = u.app.OpenURL(target) })
		items := []fyne.CanvasObject{u.secondary(backgroundBehavior(), fyne.TextAlignLeading)}
		items = append(items, startup, u.secondary("This changes future automatic starts. Current sharing keeps running. Connections you stop stay stopped until you start them again.", fyne.TextAlignLeading))
		var settings dialog.Dialog
		stop := widget.NewButton("Stop sharing", func() { settings.Hide(); u.stopAll() })
		source := widget.NewButton("View source", func() { target, _ := url.Parse(buildinfo.Current().SourceURL); _ = u.app.OpenURL(target) })
		items = append(items, stop, update, source, problem, u.secondary("Model Uplink "+Version, fyne.TextAlignLeading))
		settings = dialog.NewCustom("Settings", "Close", container.NewVBox(items...), u.window)
		settings.Show()
	})
	if u.working() {
		button.Disable()
	}
	return []fyne.CanvasObject{button}
}

func (u *ui) startupControl(problem *widget.Label) *widget.Check {
	startup := widget.NewCheck(startupLabel(), nil)
	startup.SetChecked(u.startupEnabled)
	var changed func(bool)
	reset := func() { startup.OnChanged = nil; startup.SetChecked(u.startupEnabled); startup.OnChanged = changed }
	changed = func(enabled bool) {
		if !u.begin("Updating startup…") {
			reset()
			return
		}
		startup.Disable()
		u.run(func() {
			value := "false"
			if enabled {
				value = "true"
			}
			reply := u.request(map[string]string{"action": "startup", "enabled": value})
			fyne.DoAndWait(func() {
				if reply != nil && reply.StartupEnabled != nil {
					u.startupEnabled = *reply.StartupEnabled
				}
				reset()
				startup.Enable()
				problem.SetText(u.errText)
				u.finish()
			})
		})
	}
	startup.OnChanged = changed
	return startup
}

func startupLabel() string {
	if flatpak.Enabled() {
		return "Start sharing when I sign in"
	}
	if runtime.GOOS == "windows" {
		return "Start sharing when I sign in to Windows"
	}
	return "Start sharing when this computer starts"
}

func backgroundBehavior() string {
	if flatpak.Enabled() {
		return "Closing the app keeps sharing running while you are signed in, with your desktop’s background permission. Sign-out may stop sharing. Stop sharing disconnects remote access and stays stopped until you start it again. Keep your model server running and this computer awake and online."
	}
	if runtime.GOOS == "windows" {
		return "Closing the window or exiting the app keeps sharing running until Windows sign-out. Stop sharing disconnects remote access; externally managed model apps keep running; it stays stopped until you start it again."
	}
	return "Closing the app keeps sharing running, including after sign-out. Automatic startup can resume active connections when this computer starts. Stop sharing disconnects remote access; externally managed model apps keep running; it stays stopped until you start it again. Keep this computer awake and online."
}

func (u *ui) stopAll() {
	if !u.begin("Stopping sharing…") {
		return
	}
	u.run(func() {
		reply := u.request(map[string]string{"action": "stop_all"})
		fyne.DoAndWait(func() {
			if reply != nil {
				u.applySharing(reply)
				u.savePaused()
				if reply.Notice != "" {
					u.errText = computerCopy(reply.Notice)
					u.message = ""
				} else {
					u.message = "Sharing stopped."
				}
			}
			u.finish()
			// A failed unload must remain visible after refreshing authoritative state.
			u.refresh(true)
		})
	})
}
