package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// Opt in on a real Mac. Only a unique, temporary sleep job is created; no
// Model Uplink connection, Ollama service, credential, or account is touched.
func TestMacStartupLifecycleIntegration(t *testing.T) {
	if os.Getenv("MODELUPLINK_LAUNCHD_TEST") != "1" {
		t.Skip("requires an interactive Mac login session")
	}
	label := fmt.Sprintf("com.modeluplink.testing.startup.%d", os.Getpid())
	target := "gui/" + strconv.Itoa(os.Getuid())
	path := filepath.Join(t.TempDir(), label+".plist")
	plist := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>/bin/sleep</string><string>3600</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/></dict></plist>`, label)
	if err := os.WriteFile(path, []byte(plist), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = serviceCommand("launchctl", "bootout", target+"/"+label)
		_ = setServiceStartup("darwin", label, true)
	})
	pid := func() string {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			out, err := serviceCommand("launchctl", "print", target+"/"+label)
			if err == nil {
				if m := regexp.MustCompile(`(?m)^\s*pid = ([0-9]+)$`).FindSubmatch(out); len(m) > 1 {
					return string(m[1])
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("temporary job did not start")
		return ""
	}
	if err := startLaunchAgent(target, label, path, false); err != nil {
		t.Fatal(err)
	}
	first := pid()
	for _, enabled := range []bool{true, false} {
		if err := setServiceStartup("darwin", label, enabled); err != nil {
			t.Fatal(err)
		}
		if pid() != first {
			t.Fatal("startup toggle restarted the running service")
		}
	}
	if _, err := serviceCommand("launchctl", "bootout", target+"/"+label); err != nil {
		t.Fatal(err)
	}
	if err := waitLaunchAgentGone(target + "/" + label); err != nil {
		t.Fatal(err)
	}
	if _, err := serviceCommand("launchctl", "bootstrap", target, path); err == nil {
		t.Fatal("disabled automatic job was allowed to load")
	}
	if err := startLaunchAgent(target, label, path, false); err != nil {
		t.Fatal("manual Start failed with automatic startup disabled", err)
	}
	if pid() == first {
		t.Fatal("manual Start did not load a fresh process")
	}
}
