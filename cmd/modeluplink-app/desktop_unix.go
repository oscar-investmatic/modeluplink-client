//go:build !windows

package main

import (
	"fyne.io/fyne/v2"
	"github.com/oscar-investmatic/modeluplink-client/internal/flatpak"
)

func desktopInstance() (func(), bool) { return func() {}, true }
func configureDesktop(a fyne.App, w fyne.Window, u *ui) bool {
	if !flatpak.Enabled() {
		configureTray(a, w, u)
	}
	// Some Linux desktops have no tray host. Closing must never strand a hidden
	// window; main installs its close/quit handler while services keep running.
	return false
}

func runDesktopWindow(_ fyne.App, w fyne.Window) { w.ShowAndRun() }
