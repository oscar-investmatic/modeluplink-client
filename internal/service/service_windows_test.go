package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
)

func TestWindowsTaskCredentialsStayOutOfDefinitionAndArguments(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	if err := localconfig.Save(localconfig.Config{AccountToken: "private-account-token", StartupDisabled: true}); err != nil {
		t.Fatal(err)
	}
	previous := schedulerCall
	t.Cleanup(func() { schedulerCall = previous })
	var calls []map[string]any
	schedulerCall = func(input map[string]any) error { calls = append(calls, input); return nil }
	root := t.TempDir()
	executable := filepath.Join(root, "modeluplink.exe")
	if err := os.WriteFile(filepath.Join(root, "modeluplink-app.exe"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "Name & Space", "endpoint.json")
	if _, err := Install(executable, config, "test-endpoint"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("registrations = %d", len(calls))
	}
	for _, call := range calls {
		xml := call["xml"].(string)
		if strings.Contains(xml, "private-account-token") || !strings.Contains(xml, "<Enabled>false</Enabled>") {
			t.Fatal("task leaked credentials or ignored disabled startup")
		}
	}
	if calls[0]["run"] != true || calls[1]["run"] != false {
		t.Fatal("incorrect initial task launch")
	}
	if err := Stop("test-endpoint"); err != nil {
		t.Fatal(err)
	}
	if calls[len(calls)-1]["action"] != "stop" {
		t.Fatal("stop must disable and remove the task")
	}
}
func TestWindowsTaskNamesCannotCollideWithRuntimeOrOtherUsers(t *testing.T) {
	agent, sid, err := taskName("ollama")
	if err != nil {
		t.Fatal(err)
	}
	runtime, _, _ := taskName("@ollama")
	if agent == runtime || !strings.Contains(agent, sid) {
		t.Fatal("task ownership or namespace collision")
	}
	if _, _, err := taskName("../escape"); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
}
func TestWindowsInstallRollsBackAgentIfTrayTaskFails(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "modeluplink-app.exe"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	previous := schedulerCall
	t.Cleanup(func() { schedulerCall = previous })
	var actions []string
	schedulerCall = func(input map[string]any) error {
		actions = append(actions, input["action"].(string))
		if len(actions) == 2 {
			return errors.New("registration failed")
		}
		return nil
	}
	if _, err := Install(filepath.Join(root, "modeluplink.exe"), filepath.Join(root, "endpoint.json"), "test-endpoint"); err == nil {
		t.Fatal("failure hidden")
	}
	if strings.Join(actions, ",") != "register,register,stop" {
		t.Fatalf("agent rollback missing: %v", actions)
	}
}

// Hosted CI owns a disposable Windows account. Exercise the actual COM/XML
// registration and removal, without running an endpoint or touching user tasks.
func TestWindowsSchedulerRegistrationIntegration(t *testing.T) {
	if os.Getenv("MODELUPLINK_WINDOWS_TASK_TEST") != "1" {
		t.Skip("requires disposable Windows CI account")
	}
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	if err := localconfig.Save(localconfig.Config{StartupDisabled: true}); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	slug := fmt.Sprintf("ci-scheduler-%d", os.Getpid())
	t.Cleanup(func() {
		if err := Stop(slug); err != nil {
			t.Error(err)
		}
	})
	if err := registerTask(slug, executable, []string{"_desktop"}, false); err != nil {
		t.Fatal(err)
	}
	if err := Stop(slug); err != nil {
		t.Fatal(err)
	}
	if err := Stop(slug); err != nil {
		t.Fatalf("idempotent cleanup: %v", err)
	}
}
