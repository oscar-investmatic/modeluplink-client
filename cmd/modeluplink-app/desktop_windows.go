package main

import (
	"fyne.io/fyne/v2"
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

func desktopInstance() (func(), bool) {
	name, _ := windows.UTF16PtrFromString(`Local\ModelUplink-UI`)
	handle, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(handle)
		title, _ := windows.UTF16PtrFromString(windowTitle)
		user32 := windows.NewLazySystemDLL("user32.dll")
		window, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
		if window != 0 {
			user32.NewProc("ShowWindow").Call(window, 9)
			user32.NewProc("SetForegroundWindow").Call(window)
		}
		return func() {}, false
	}
	if err != nil {
		message, _ := windows.UTF16PtrFromString("Model Uplink could not start. Please try opening it again.")
		title, _ := windows.UTF16PtrFromString(windowTitle)
		windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
		return func() {}, false
	}
	return func() { windows.CloseHandle(handle) }, true
}
func configureDesktop(a fyne.App, w fyne.Window, u *ui) bool { return configureTray(a, w, u) }

func runDesktopWindow(a fyne.App, w fyne.Window) {
	if len(os.Args) > 1 && os.Args[1] == "--background" {
		a.Run()
	} else {
		w.ShowAndRun()
	}
}
