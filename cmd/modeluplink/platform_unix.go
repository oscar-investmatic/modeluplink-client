//go:build !windows

package main

func platformCommand(string) (bool, error) { return false, nil }

func followWindowsLog(string) error    { return nil }
func stopWindowsEndpoint(string) error { return nil }
