package hostexec

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestManagedJobKillsDescendantWhenRuntimeExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	file, err := os.CreateTemp(t.TempDir(), "child-pid")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cmd := CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop';$p=Start-Process powershell.exe -WindowStyle Hidden -ArgumentList @('-NoProfile','-NonInteractive','-Command','Start-Sleep -Seconds 90') -PassThru;[Console]::Out.Write($p.Id)`)
	cmd.Stdout = file
	if err := RunInJob(cmd); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 32)
	if err != nil {
		t.Fatal("missing child PID")
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err == windows.ERROR_INVALID_PARAMETER {
		return
	} // Already reaped.
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	status, err := windows.WaitForSingleObject(process, 5000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("managed descendant survived runtime exit: %v status %d", err, status)
	}
}
