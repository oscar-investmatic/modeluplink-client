package main

import (
	"golang.org/x/sys/windows"
)

func openWindowsBrowser(target string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	url, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, url, nil, nil, windows.SW_SHOWNORMAL)
}
