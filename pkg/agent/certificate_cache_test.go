package agent

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/crypto/acme/autocert"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCertificateCacheProtectsPrivateKeys(t *testing.T) {
	dir := t.TempDir()
	cache := certificateCache(dir)
	ctx := context.Background()
	secret := []byte("private-acme-account-and-certificate-key")
	if err := cache.Put(ctx, "acme_account+key", secret); err != nil {
		t.Fatal(err)
	}
	got, err := cache.Get(ctx, "acme_account+key")
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("roundtrip: %v", err)
	}
	if runtime.GOOS == "windows" {
		raw, err := os.ReadFile(filepath.Join(dir, "acme_account+key"))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, secret) {
			t.Fatal("TLS private key stored in plaintext")
		}
	}
	if err := cache.Delete(ctx, "acme_account+key"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Get(ctx, "acme_account+key"); !errors.Is(err, autocert.ErrCacheMiss) {
		t.Fatalf("deleted key: %v", err)
	}
}
