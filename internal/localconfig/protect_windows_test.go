package localconfig

import (
	"bytes"
	"os"
	"testing"
)

func TestWindowsCredentialsAreDPAPIProtected(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	cfg := Config{AccountToken: "private-account-token", Endpoints: map[string]Endpoint{}}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	path, _ := Path()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(cfg.AccountToken)) || !bytes.HasPrefix(data, protectedPrefix) {
		t.Fatal("account token stored without DPAPI")
	}
	loaded, err := Load()
	if err != nil || loaded.AccountToken != cfg.AccountToken {
		t.Fatalf("round trip failed: %v", err)
	}
	e := Endpoint{Slug: "windows-test", AgentToken: "private-agent-token", UpstreamKey: "private-upstream-key"}
	endpointPath, err := SaveEndpoint(e)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(endpointPath)
	if bytes.Contains(data, []byte(e.AgentToken)) || bytes.Contains(data, []byte(e.UpstreamKey)) {
		t.Fatal("endpoint secret in plaintext")
	}
	got, err := LoadEndpoint(endpointPath)
	if err != nil || got.AgentToken != e.AgentToken {
		t.Fatalf("endpoint decryption failed: %v", err)
	}
	data[len(data)-1] ^= 1
	_ = os.WriteFile(endpointPath, data, 0600)
	if _, err = LoadEndpoint(endpointPath); err == nil {
		t.Fatal("modified protected payload accepted")
	}
	if _, err = unprotectPayload([]byte(`{"account_token":"plaintext"}`)); err == nil {
		t.Fatal("unprotected legacy file accepted")
	}
}
