package main

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// Inference keys use Secret Service on Linux or Credential Manager on Windows,
// keyed by endpoint slug. The helper owns account-session storage; the GUI keeps
// newly issued inference keys in memory so a keyring failure does not lose them.
const keyringService = "Model Uplink"

var errKeyring = errors.New("Your key couldn’t be saved in the system keyring. You can still copy it now.")
var errKeyNotFound = keyring.ErrNotFound
var errKeyringRead = errors.New("Your system keyring is locked or unavailable. Unlock it and try copying your key again.")

type keyStore interface {
	save(slug, key string) error
	read(slug string) (string, error)
	remove(slug string)
}

type ringStore struct{}

func (ringStore) save(slug, key string) error {
	if err := keyring.Set(keyringService, slug, key); err != nil {
		return errKeyring
	}
	return nil
}

func (ringStore) read(slug string) (string, error) {
	key, err := keyring.Get(keyringService, slug)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", errKeyNotFound
	}
	if err != nil || key == "" {
		return "", errKeyringRead
	}
	return key, nil
}

func (ringStore) remove(slug string) {
	_ = keyring.Delete(keyringService, slug)
}
