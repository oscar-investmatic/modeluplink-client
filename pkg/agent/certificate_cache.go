package agent

import (
	"context"
	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"golang.org/x/crypto/acme/autocert"
)

// The cache includes ACME account keys and endpoint private keys. Protect them
// with the signed-in user's DPAPI key on Windows; Unix retains its 0700 cache.
type certificateCache string

func (c certificateCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := autocert.DirCache(c).Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return localconfig.UnprotectSecrets(data)
}
func (c certificateCache) Put(ctx context.Context, key string, data []byte) error {
	if err := localconfig.PrivateDirectory(string(c)); err != nil {
		return err
	}
	protected, err := localconfig.ProtectSecrets(data)
	if err != nil {
		return err
	}
	return autocert.DirCache(c).Put(ctx, key, protected)
}
func (c certificateCache) Delete(ctx context.Context, key string) error {
	return autocert.DirCache(c).Delete(ctx, key)
}
