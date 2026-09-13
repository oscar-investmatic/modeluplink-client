//go:build !windows

package service

import (
	"context"
	"errors"
)

func RedirectAgentLog(string) (func(), error) { return func() {}, nil }
func RunManagedOllama(context.Context) error {
	return errors.New("Windows runtime runner is unavailable on this platform")
}
