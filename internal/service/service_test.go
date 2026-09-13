//go:build !windows

package service

import (
	"strings"
	"testing"
)

func TestSystemdUnitKeepsOnlyCertificateCacheWritable(t *testing.T) {
	unit := systemdUnit("/opt/model uplink/modeluplink", "/home/person/.config/modeluplink/endpoints/home-gpu.json", "/home/person/.config/modeluplink/endpoints/home-gpu-certificates", "home-gpu")
	for _, required := range []string{
		"ProtectSystem=strict",
		"ProtectHome=read-only",
		`ReadWritePaths="/home/person/.config/modeluplink/endpoints/home-gpu-certificates"`,
		`ExecStart="/opt/model uplink/modeluplink" _agent --config "/home/person/.config/modeluplink/endpoints/home-gpu.json"`,
	} {
		if !strings.Contains(unit, required) {
			t.Fatalf("unit does not contain %q:\n%s", required, unit)
		}
	}
}

func TestStopRejectsUnsafeServiceSlugBeforeExecutingCommand(t *testing.T) {
	if err := Stop("../other"); err == nil {
		t.Fatal("unsafe service slug was accepted")
	}
}
