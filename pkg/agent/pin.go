package agent

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// CertificatePin returns the SHA-256 SPKI pin for a locally cached endpoint
// certificate without exposing or copying its private key.
func CertificatePin(cacheDir, host string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", errors.New("endpoint hostname is required")
	}
	var lastErr error
	for _, name := range []string{host, host + "+rsa"} {
		data, err := certificateCache(cacheDir).Get(context.Background(), name)
		if err != nil {
			lastErr = err
			continue
		}
		for len(data) > 0 {
			block, rest := pem.Decode(data)
			if block == nil {
				lastErr = errors.New("certificate cache contains invalid PEM")
				break
			}
			data = rest
			if block.Type != "CERTIFICATE" {
				continue
			}
			certificate, parseErr := x509.ParseCertificate(block.Bytes)
			if parseErr != nil {
				lastErr = parseErr
				continue
			}
			if verifyErr := certificate.VerifyHostname(host); verifyErr != nil {
				lastErr = verifyErr
				continue
			}
			digest := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
			return "sha256/" + base64.StdEncoding.EncodeToString(digest[:]), nil
		}
	}
	if lastErr == nil {
		lastErr = errors.New("certificate was not found")
	}
	return "", fmt.Errorf("read endpoint certificate pin: %w", lastErr)
}
