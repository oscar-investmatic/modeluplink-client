package main

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// Inference keys live in the desktop keyring (Secret Service on Linux,
// Keychain on macOS) under one service, keyed by endpoint slug. The CLI's
// 0600 configuration holds the account session; the app never stores it.
const keyringService = "Model Uplink"

var errKeyring = errors.New("Your key couldn’t be saved in the system keyring. You can still copy it now.")

type keyStore interface {
	save(slug, key string) error
	read(slug string) (string, bool)
	remove(slug string)
}

type ringStore struct{}

func (ringStore) save(slug, key string) error {
	if err := keyring.Set(keyringService, slug, key); err != nil {
		return errKeyring
	}
	return nil
}

func (ringStore) read(slug string) (string, bool) {
	key, err := keyring.Get(keyringService, slug)
	if err != nil || key == "" {
		return "", false
	}
	return key, true
}

func (ringStore) remove(slug string) {
	_ = keyring.Delete(keyringService, slug)
}
