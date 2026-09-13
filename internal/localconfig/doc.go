// Package localconfig persists account and endpoint state for the current user.
// File permissions, Windows DPAPI, and keyring references protect different
// credential types; see docs/architecture.md for the storage boundary.
package localconfig
