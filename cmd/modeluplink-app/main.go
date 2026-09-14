// Model Uplink desktop app: a native window over the modeluplink CLI's
// `_desktop` helper protocol. It signs the user in with an emailed code,
// connects selected local models to an HTTPS endpoint, and shows its address
// and API key. See internal/desktopcontract for the helper protocol fixtures.
package main

import (
	_ "embed"
	"image/color"
	"os"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
)

//go:embed icon.png
var iconPNG []byte

var appIcon = fyne.NewStaticResource("icon.png", iconPNG)

const (
	appID         = "com.modeluplink.app"
	windowTitle   = "Model Uplink"
	defaultWidth  = 520
	defaultHeight = 680
	minWidth      = 420
	minHeight     = 560
)

const updateInstalledNotice = "An update is installed. Close this window, then open Model Uplink again and choose Restart to update. Sharing keeps running until then."

// uplinkTheme keeps the Mac app's dark, lime-accented look regardless of the
// desktop's light or dark preference.
type uplinkTheme struct{ fyne.Theme }

var (
	limeColor       = color.NRGBA{R: 168, G: 255, B: 79, A: 255}
	backgroundColor = color.NRGBA{R: 9, G: 14, B: 11, A: 255}
	mutedColor      = color.NRGBA{R: 158, G: 176, B: 166, A: 255}
)

func (t uplinkTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNamePrimary:
		return limeColor
	case theme.ColorNameForegroundOnPrimary:
		return backgroundColor
	case theme.ColorNameBackground:
		return backgroundColor
	case theme.ColorNameDisabled, theme.ColorNamePlaceHolder:
		// Secondary text and hints: Fyne's dark "disabled" gray sits under
		// 3:1 against this background; this keeps them muted but readable.
		return mutedColor
	}
	return t.Theme.Color(name, theme.VariantDark)
}

// Version is the application version injected by the platform packaging scripts.
var Version = "dev"

func main() {
	if flatpak.Enabled() && len(os.Args) == 2 && os.Args[1] == "--flatpak-update" {
		if runUpdateWindow() {
			os.Exit(flatpak.UpdateAcceptedExit)
		}
		return
	}
	release, ok := desktopInstance()
	if !ok {
		return
	}
	defer release()
	a := app.NewWithID(appID)
	a.SetIcon(appIcon)
	a.Settings().SetTheme(uplinkTheme{theme.DefaultTheme()})
	w := a.NewWindow(windowTitle)
	w.SetIcon(appIcon)
	w.Resize(fyne.NewSize(defaultWidth, defaultHeight))
	w.SetMaster()
	u := newUI(a, w, &processHelper{}, ringStore{})
	// Fyne derives the window's minimum size from its content.
	floor := canvas.NewRectangle(color.Transparent)
	floor.SetMinSize(fyne.NewSize(minWidth, minHeight))
	w.SetContent(container.NewStack(floor, u.content))
	u.refresh(false)
	u.startBackgroundRefresh()
	if flatpak.Enabled() {
		u.run(func() {
			flatpak.WatchUpdate(u.ctx, 5*time.Second, func() {
				fyne.Do(func() { u.updateInstalled = true; u.render() })
			})
		})
	}
	if !configureDesktop(a, w, u) {
		w.SetCloseIntercept(func() {
			if u.working() {
				dialog.ShowInformation("Finishing up", "Please let this step finish before closing Model Uplink.", w)
				return
			}
			u.stop()
			w.Close()
		})
	}
	// Window placement is left to the window manager: Wayland ignores
	// requests to center, and glfw crashes on a session with no monitor.
	runDesktopWindow(a, w)
}
