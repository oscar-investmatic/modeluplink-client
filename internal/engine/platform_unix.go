//go:build !windows

package engine

import (
	"context"
	"errors"
)

func installWindows(context.Context) error {
	return errors.New("Windows runtime is unavailable on this platform")
}
func windowsOllamaPaths() []string { return nil }
func verifyWindowsLoopback() error { return nil }
