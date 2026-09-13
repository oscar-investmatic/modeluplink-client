// A test-only credential probe. Built separately; never included in the app bundle.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/zalando/go-keyring"
	"os"
)

func main() {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	label := "flatpak-check-" + hex.EncodeToString(value[:8])
	secret := hex.EncodeToString(value)
	const service = "com.modeluplink.flatpak-test"
	if err := keyring.Set(service, label, secret); err != nil {
		fmt.Fprintln(os.Stderr, "keyring write:", err)
		os.Exit(1)
	}
	defer keyring.Delete(service, label)
	stored, err := keyring.Get(service, label)
	if err != nil || stored != secret {
		fmt.Fprintln(os.Stderr, "keyring read failed")
		os.Exit(1)
	}
	if err = keyring.Delete(service, label); err != nil {
		fmt.Fprintln(os.Stderr, "keyring cleanup failed")
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"check": "sandbox Secret Service write/read/delete", "passed": true})
}
