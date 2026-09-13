package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
)

const secretBytes = 32

func NewID(prefix string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func NewToken(prefix, id string) (plain string, secret string, err error) {
	b := make([]byte, secretBytes)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	secret = base64.RawURLEncoding.EncodeToString(b)
	return prefix + "_" + id + "_" + secret, secret, nil
}

func ParseToken(token, prefix string) (id, secret string, err error) {
	marker := prefix + "_"
	if !strings.HasPrefix(token, marker) {
		return "", "", errors.New("invalid token format")
	}
	remainder := strings.TrimPrefix(token, marker)
	// Generated secrets are 32 bytes encoded with base64.RawURLEncoding (43
	// characters). Locate the delimiter by length because both IDs and the
	// base64url alphabet may themselves contain underscores.
	separator := len(remainder) - 44
	if separator < 1 || separator >= len(remainder)-1 || remainder[separator] != '_' {
		return "", "", errors.New("invalid token format")
	}
	return remainder[:separator], remainder[separator+1:], nil
}

func HashSecret(secret string, pepper []byte) []byte {
	h := hmac.New(sha256.New, pepper)
	_, _ = h.Write([]byte(secret))
	return h.Sum(nil)
}

func VerifySecret(secret string, expected, pepper []byte) bool {
	actual := HashSecret(secret, pepper)
	return len(actual) == len(expected) && subtle.ConstantTimeCompare(actual, expected) == 1
}

// HashInferenceSecret creates the verifier used for endpoint inference keys.
// These secrets contain 256 random bits, so a public, domain-separated digest
// remains infeasible to brute force while allowing an agent to prove a bearer
// key locally without sending the reusable secret to the control plane.
func HashInferenceSecret(id, secret string) []byte {
	digest := sha256.Sum256([]byte("modeluplink-inference-v1\x00" + id + "\x00" + secret))
	return digest[:]
}

func InferenceProof(id, secret string) string {
	return base64.RawURLEncoding.EncodeToString(HashInferenceSecret(id, secret))
}

func VerifyInferenceProof(proof string, expected []byte) bool {
	actual, err := base64.RawURLEncoding.DecodeString(proof)
	return err == nil && len(actual) == len(expected) && subtle.ConstantTimeCompare(actual, expected) == 1
}
