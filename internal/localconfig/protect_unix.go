//go:build !windows

package localconfig

import "os"

func protectPayload(data []byte) ([]byte, error)   { return data, nil }
func unprotectPayload(data []byte) ([]byte, error) { return data, nil }
func PrivateDirectory(path string) error           { return os.MkdirAll(path, 0700) }
