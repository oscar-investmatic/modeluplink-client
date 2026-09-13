package localconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteEndpointFilesRemovesOnlyValidatedEndpointState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MODELUPLINK_CONFIG_DIR", filepath.Join(root, "config", "modeluplink"))
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	path, err := SaveEndpoint(Endpoint{ID: "ep_test", Slug: "home-gpu"})
	if err != nil {
		t.Fatal(err)
	}
	certificateDir := filepath.Join(filepath.Dir(path), "home-gpu-certificates")
	if err = os.MkdirAll(certificateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(certificateDir, "home-gpu.modeluplink.test"), []byte("private key"), 0600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(filepath.Dir(path), "keep.txt")
	if err = os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = DeleteEndpointFiles("home-gpu"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("endpoint config remains: %v", err)
	}
	if _, err = os.Stat(certificateDir); !os.IsNotExist(err) {
		t.Fatalf("certificate directory remains: %v", err)
	}
	if _, err = os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
	if err = DeleteEndpointFiles("../other"); err == nil {
		t.Fatal("unsafe endpoint slug was accepted")
	}
}
