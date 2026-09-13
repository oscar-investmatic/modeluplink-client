package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestCertificatePinReadsOnlyMatchingCachedCertificate(t *testing.T) {
	const host = "home-gpu.modeluplink.test"
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	payload := append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	cache := t.TempDir()
	if err := certificateCache(cache).Put(context.Background(), host, payload); err != nil {
		t.Fatal(err)
	}

	pin, err := CertificatePin(cache, host)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pin, "sha256/") || len(pin) != len("sha256/")+44 {
		t.Fatalf("pin = %q", pin)
	}
	if _, err := CertificatePin(cache, "other.modeluplink.test"); err == nil {
		t.Fatal("expected another hostname to be rejected")
	}
}
