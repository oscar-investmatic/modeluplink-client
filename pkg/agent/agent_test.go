package agent

import "testing"

func TestValidateUpstream(t *testing.T) {
	valid := []string{"http://127.0.0.1:11434", "http://localhost:8000", "http://[::1]:8000"}
	for _, raw := range valid {
		if err := ValidateUpstream(raw, false); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	invalid := []string{"file:///tmp/socket", "http://192.168.1.5:8000", "http://user:pass@localhost:8000", "http://localhost:8000?admin=true"}
	for _, raw := range invalid {
		if err := ValidateUpstream(raw, false); err == nil {
			t.Errorf("expected %s to be rejected", raw)
		}
	}
	if err := ValidateUpstream("http://192.168.1.5:8000", true); err != nil {
		t.Fatalf("explicit LAN upstream rejected: %v", err)
	}
}
