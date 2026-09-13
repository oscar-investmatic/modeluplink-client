package main

import (
	"errors"
	"testing"
)

type unavailableKeys struct{ testKeys }

func (unavailableKeys) read(string) (string, error) {
	return "", errors.New("unlock cancelled")
}

func TestCancelledKeyringUnlockDoesNotCreateKey(t *testing.T) {
	u, h := newTestUI(t)
	u.keys = unavailableKeys{}
	u.copyKey(endpoint{Slug: "test-model"})
	if u.errText != errKeyringRead.Error() {
		t.Fatalf("missing recovery message: %q", u.errText)
	}
	if len(h.calls) != 0 || u.working() {
		t.Fatal("keyring failure started a key creation request")
	}
}
