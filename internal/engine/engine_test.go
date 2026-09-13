package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProbeAcceptsVersionedBaseURL(t *testing.T) {
	for _, basePath := range []string{"", "/", "/v1", "/v1/", "/proxy", "/proxy/v1/"} {
		t.Run(basePath, func(t *testing.T) {
			prefix := ""
			if basePath == "/proxy" || basePath == "/proxy/v1/" {
				prefix = "/proxy"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != prefix+"/v1/models" {
					t.Errorf("upstream path = %q", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
			}))
			defer server.Close()
			models, err := Probe(context.Background(), server.URL+basePath)
			if err != nil || len(models) != 1 || models[0] != "test-model" {
				t.Fatalf("models=%v err=%v", models, err)
			}
		})
	}
}

func TestDownloadVerifiedFileChecksDigestAndSize(t *testing.T) {
	payload := []byte("verified ollama archive")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v"+ollamaVersion+"/archive.test" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	path, err := downloadVerifiedFileFrom(context.Background(), ollamaArtifact{name: "archive.test", sha256: digest, maxBytes: int64(len(payload))}, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("payload = %q, err = %v", got, err)
	}
	if _, err = downloadVerifiedFileFrom(context.Background(), ollamaArtifact{name: "archive.test", sha256: digest, maxBytes: int64(len(payload) - 1)}, server.URL, server.Client()); err == nil {
		t.Fatal("oversized archive was accepted")
	}
	if _, err = downloadVerifiedFileFrom(context.Background(), ollamaArtifact{name: "archive.test", sha256: "wrong", maxBytes: 1024}, server.URL, server.Client()); err == nil {
		t.Fatal("archive with wrong digest was accepted")
	}
}

func TestLinkManagedOllamaNeverOverwritesUnmanagedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink installation; Windows uses versioned directories")
	}
	home := t.TempDir()
	versionOne := filepath.Join(home, ".local", "share", "modeluplink", "ollama", "one")
	versionTwo := filepath.Join(home, ".local", "share", "modeluplink", "ollama", "two")
	for _, version := range []string{versionOne, versionTwo} {
		if err := os.MkdirAll(filepath.Join(version, "bin"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(version, "bin", "ollama"), []byte("binary"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := linkManagedOllama(home, versionOne); err != nil {
		t.Fatal(err)
	}
	if err := linkManagedOllama(home, versionTwo); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".local", "bin", "ollama")
	target, err := os.Readlink(link)
	if err != nil || target != filepath.Join(versionTwo, "bin", "ollama") {
		t.Fatalf("link target = %q, err = %v", target, err)
	}
	if err = os.Remove(link); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(home, "other", "ollama")
	if err = os.MkdirAll(filepath.Dir(unmanaged), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(unmanaged, []byte("unmanaged"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(unmanaged, link); err != nil {
		t.Fatal(err)
	}
	if err = linkManagedOllama(home, versionOne); err == nil {
		t.Fatal("unmanaged symlink was replaced")
	}
}

func TestManagedOllamaDownloadDoesNotExecuteInstallerScript(t *testing.T) {
	artifact := ollamaArtifacts["linux/amd64"]
	if filepath.Ext(artifact.name) == ".sh" {
		t.Fatalf("Linux artifact must be a binary archive, got %q", artifact.name)
	}
	if artifact.sha256 == "" || artifact.maxBytes < 1<<30 {
		t.Fatalf("Linux artifact metadata is incomplete: %#v", artifact)
	}
}

func TestListenerOutputRejectsNonLoopbackOllama(t *testing.T) {
	for _, test := range []struct {
		kind, output string
		want         bool
	}{
		{"ss", "LISTEN 0 4096 127.0.0.1:11434 0.0.0.0:*", false},
		{"ss", "LISTEN 0 4096 [::1]:11434 [::]:*", false},
		{"ss", "LISTEN 0 4096 0.0.0.0:11434 0.0.0.0:*", true},
		{"ss", "LISTEN 0 4096 192.168.1.10:11434 0.0.0.0:*", true},
		{"lsof", "ollama 123 user 3u IPv4 0t0 TCP 127.0.0.1:11434 (LISTEN)", false},
		{"lsof", "ollama 123 user 3u IPv6 0t0 TCP *:11434 (LISTEN)", true},
	} {
		if got := listenerOutputHasNonLoopback(test.kind, test.output); got != test.want {
			t.Errorf("%s %q: got %v, want %v", test.kind, test.output, got, test.want)
		}
	}
}
