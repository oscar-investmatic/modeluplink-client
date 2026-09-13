//go:build windows

package flatpak

import "errors"

func Run(bool) error { return errors.New("Flatpak is only supported on Linux") }
