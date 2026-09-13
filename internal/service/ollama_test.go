//go:build !windows

package service

import (
	encodingxml "encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestManagedOllamaPlistEscapesPaths(t *testing.T) {
	content := ollamaPlist(`/Users/a & b/ollama`, `/Users/a & b/models`, `/Users/a & b/ollama.log`)
	decoder := encodingxml.NewDecoder(strings.NewReader(content))
	values := []string{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if data, ok := token.(encodingxml.CharData); ok {
			values = append(values, string(data))
		}
	}
	for _, want := range []string{`/Users/a & b/ollama`, `127.0.0.1:11434`, `/Users/a & b/models`, `/Users/a & b/ollama.log`} {
		found := false
		for _, value := range values {
			if value == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestLingerAlreadyEnabledDoesNotMutate(t *testing.T) {
	previous := serviceCommand
	t.Cleanup(func() { serviceCommand = previous })
	calls := 0
	serviceCommand = func(name string, args ...string) ([]byte, error) {
		calls++
		if name != "loginctl" || args[0] != "show-user" {
			t.Fatal(name, args)
		}
		return []byte("yes\n"), nil
	}
	if err := ensureLinger(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestLingerPermissionFailureIsActionable(t *testing.T) {
	previous := serviceCommand
	t.Cleanup(func() { serviceCommand = previous })
	serviceCommand = func(name string, args ...string) ([]byte, error) {
		if args[0] == "show-user" {
			return []byte("no\n"), nil
		}
		return []byte("Access denied"), errors.New("exit 1")
	}
	if err := ensureLinger(); err == nil || !strings.Contains(err.Error(), "sudo loginctl enable-linger") {
		t.Fatal(err)
	}
}

func TestWaitLaunchAgentGoneWaitsForRegistrationRemoval(t *testing.T) {
	previous := serviceCommand
	defer func() { serviceCommand = previous }()
	calls := 0
	serviceCommand = func(name string, args ...string) ([]byte, error) {
		calls++
		if name != "launchctl" || len(args) != 2 || args[0] != "print" {
			t.Fatal("unexpected service command")
		}
		if calls < 3 {
			return nil, nil
		}
		return nil, errors.New("service not found")
	}
	if err := waitLaunchAgentGone("gui/501/com.modeluplink.test"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("did not wait for removal")
	}
}
