package localconfig

import (
	"errors"
	"github.com/zalando/go-keyring"
)

const upstreamSecretService = "com.modeluplink.upstream"

var storeUpstreamSecret = keyring.Set
var readUpstreamSecret = keyring.Get

// Only new externally managed endpoints move credentials into the OS vault.
// Legacy configurations retain their existing representation until migrated.
func protectUpstream(e Endpoint) (Endpoint, error) {
	if e.RuntimeOwnership != "external" || e.UpstreamKey == "" {
		return e, nil
	}
	if e.ID == "" {
		return e, errors.New("The connection needs an identity before its key can be saved.")
	}
	ref := "endpoint-" + e.ID
	if e.UpstreamKeyRef == "" {
		if err := storeUpstreamSecret(upstreamSecretService, ref, e.UpstreamKey); err != nil {
			return e, errors.New("Unlock your system credential store to save the local server API key.")
		}
	}
	e.UpstreamKeyRef = ref
	e.UpstreamKey = ""
	return e, nil
}

// ResolveUpstreamKey is called only for operations that contact this endpoint.
// A locked vault must not prevent stopping or deleting a connection.
func ResolveUpstreamKey(e Endpoint) (string, error) {
	if e.UpstreamKeyRef == "" {
		return e.UpstreamKey, nil
	}
	key, err := readUpstreamSecret(upstreamSecretService, e.UpstreamKeyRef)
	if err != nil {
		return "", errors.New("Unlock your system credential store to access the local server API key.")
	}
	return key, nil
}
