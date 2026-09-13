package localconfig

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestExternalCredentialsStoredOutsideConfiguration(t *testing.T) {
	t.Setenv("MODELUPLINK_CONFIG_DIR", t.TempDir())
	oldSet, oldGet := storeUpstreamSecret, readUpstreamSecret
	defer func() { storeUpstreamSecret = oldSet; readUpstreamSecret = oldGet }()
	vault := map[string]string{}
	storeUpstreamSecret = func(service, ref, key string) error { vault[ref] = key; return nil }
	readUpstreamSecret = func(service, ref string) (string, error) { return vault[ref], nil }
	e := Endpoint{ID: "ep_external", Slug: "local-source", Engine: "openai_compatible", RuntimeOwnership: "external", UpstreamKey: "never-in-json"}
	path, err := SaveEndpoint(e)
	if err != nil {
		t.Fatal(err)
	}
	if err = Save(Config{Endpoints: map[string]Endpoint{e.Slug: e}}); err != nil {
		t.Fatal(err)
	}
	cfgPath, _ := Path()
	for _, p := range []string{path, cfgPath} {
		data, _ := os.ReadFile(p)
		data, _ = UnprotectSecrets(data)
		if strings.Contains(string(data), e.UpstreamKey) {
			t.Fatal("local API credential leaked into config")
		}
	}
	saved, err := LoadEndpoint(path)
	if err != nil || saved.UpstreamKey != "" || saved.UpstreamKeyRef == "" {
		t.Fatalf("saved state %v", err)
	}
	key, err := ResolveUpstreamKey(saved)
	if err != nil || key != e.UpstreamKey {
		t.Fatal("credential not resolved")
	}
	readUpstreamSecret = func(string, string) (string, error) { return "", errors.New("locked") }
	if _, err = Load(); err != nil {
		t.Fatal("locked vault prevented managing connections")
	}
	if _, err = ResolveUpstreamKey(saved); err == nil {
		t.Fatal("locked vault silently lost auth")
	}
	if e.ManagesRuntime() {
		t.Fatal("external runtime managed")
	}
	e.Engine = "ollama"
	if e.ManagesRuntime() {
		t.Fatal("attached Ollama managed")
	}
	e.RuntimeOwnership = ""
	if !e.ManagesRuntime() {
		t.Fatal("legacy behavior changed")
	}
}
