//go:build !windows

package main

import "errors"

func openWindowsBrowser(string) error { return errors.New("unsupported platform") }
