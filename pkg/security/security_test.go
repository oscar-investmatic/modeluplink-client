package security

import (
	"strings"
	"testing"
)

func TestTokenRoundTripWithUnderscoreID(t *testing.T) {
	plain, secret, err := NewToken("mup", "key_abc123")
	if err != nil {
		t.Fatal(err)
	}
	id, parsed, err := ParseToken(plain, "mup")
	if err != nil {
		t.Fatal(err)
	}
	if id != "key_abc123" || parsed != secret {
		t.Fatalf("round trip mismatch: id=%q", id)
	}
	pepper := []byte("01234567890123456789012345678901")
	hash := HashSecret(secret, pepper)
	if !VerifySecret(secret, hash, pepper) {
		t.Fatal("valid secret rejected")
	}
	if VerifySecret(secret+"x", hash, pepper) {
		t.Fatal("invalid secret accepted")
	}
}

func TestInferenceProof(t *testing.T) {
	const id = "key_abc123"
	const secret = "0123456789012345678901234567890123456789012"
	verifier := HashInferenceSecret(id, secret)
	if !VerifyInferenceProof(InferenceProof(id, secret), verifier) {
		t.Fatal("valid inference proof rejected")
	}
	if VerifyInferenceProof(InferenceProof(id, secret+"x"), verifier) {
		t.Fatal("invalid inference proof accepted")
	}
	if VerifyInferenceProof(InferenceProof("key_other", secret), verifier) {
		t.Fatal("proof was not bound to its key id")
	}
}

func TestNormalizeCORSOrigin(t *testing.T) {
	got, err := NormalizeCORSOrigin("HTTPS://Editor.Example:8443/")
	if err != nil || got != "https://editor.example:8443" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, raw := range []string{"*", "file://editor", "https://*.example.com", "https://example.com/path", "https://user@example.com"} {
		if _, err := NormalizeCORSOrigin(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestValidateSlug(t *testing.T) {
	for _, valid := range []string{"home-gpu", "abc", "rack-01", strings.Repeat("a", 48)} {
		if err := ValidateSlug(valid); err != nil {
			t.Errorf("%s should be valid: %v", valid, err)
		}
	}
	for _, invalid := range []string{"api", "a", "ab", "Home", "a--b", "-bad", "bad-", "a.b", "", strings.Repeat("a", 49)} {
		if err := ValidateSlug(invalid); err == nil {
			t.Errorf("%s should be invalid", invalid)
		}
	}
}
