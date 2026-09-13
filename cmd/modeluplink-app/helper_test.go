package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

type singleByteReader struct{ io.Reader }

func (r singleByteReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:1]) }
func TestHelperStreamFragmentedProgressAndCurrentState(t *testing.T) {
	raw := `{"event":"progress","stage":"unload","message":"Releasing model memory…"}
{"shared_models":{"test":["llama3.2:3b"]},"activity":{"test":{"model":"llama3.2:3b","running":1,"waiting":2}},"stopped":{"test":true},"memory_release_pending":{"test":true},"notice":"Retry release"}
`
	var stages []string
	reply, err := parseHelperStream(singleByteReader{strings.NewReader(raw)}, func(p progressEvent) { stages = append(stages, p.Stage) })
	if err != nil || len(stages) != 1 || stages[0] != "unload" {
		t.Fatalf("progress decode failed: %v", err)
	}
	if !reply.Stopped["test"] || !reply.MemoryReleasePending["test"] || reply.Activity["test"].Waiting != 2 || len(reply.SharedModels["test"]) != 1 {
		t.Fatal("lost shared-model or stop state")
	}
}
func TestHelperStreamRejectsInvalidOrAmbiguousReplies(t *testing.T) {
	for _, raw := range []string{"", "garbage", "{}\n{}\n", "{}\n{\"event\":\"progress\"}\n", "{\"event\":\"surprise\"}\n", strings.Repeat("x", maxHelperLine+1)} {
		if _, err := parseHelperStream(strings.NewReader(raw), nil); err == nil {
			t.Fatal("accepted invalid stream")
		}
	}
}
func TestHelperPipesNeverPutSecretsInArguments(t *testing.T) {
	// Re-execute this native test binary to check the real OS pipe contract.
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MODELUPLINK_PIPE_TEST", "1")
	t.Setenv("MODELUPLINK_ACCOUNT_TOKEN", "must-not-leak")
	h := &processHelper{locate: func() (string, error) { return path, nil }}
	reply, err := h.run(context.Background(), map[string]string{"action": "verify_code", "code": "private-test-code"}, nil)
	if err != nil || !reply.Ready {
		t.Fatalf("pipe request failed: %v", err)
	}
	if requestTimeout("share_models") < 8*90*time.Second {
		t.Fatal("multi-model check can be killed too early")
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("MODELUPLINK_PIPE_TEST") == "1" {
		if len(os.Args) != 2 || os.Args[1] != "_desktop" {
			os.Exit(2)
		}
		if os.Getenv("MODELUPLINK_ACCOUNT_TOKEN") != "" {
			os.Exit(3)
		}
		body, err := io.ReadAll(os.Stdin)
		if err != nil || !strings.Contains(string(body), "private-test-code") {
			os.Exit(4)
		}
		_, _ = io.WriteString(os.Stdout, "{\"ready\":true}\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
