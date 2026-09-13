package engine

import (
	"net"
	"os"
	"testing"
)

func TestWindowsLoopbackGuardChecksActualListeners(t *testing.T) {
	if os.Getenv("MODELUPLINK_WINDOWS_TASK_TEST") != "1" {
		t.Skip("requires disposable Windows CI account")
	}
	for _, host := range []string{"127.0.0.1", "0.0.0.0"} {
		listener, err := net.Listen("tcp4", host+":11434")
		if err != nil {
			t.Fatal(err)
		}
		err = verifyWindowsLoopback()
		listener.Close()
		if (err == nil) != (host == "127.0.0.1") {
			t.Fatalf("listener %s: %v", host, err)
		}
	}
}
func TestWindowsHardwareDetection(t *testing.T) {
	if detectRAM() == 0 || detectStorage() == 0 {
		t.Fatal("Windows memory or free storage detection failed")
	}
}
