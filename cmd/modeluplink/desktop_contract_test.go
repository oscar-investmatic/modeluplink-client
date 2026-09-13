package main

import (
	"encoding/json"
	"testing"

	"github.com/oscar-investmatic/modeluplink-client/internal/desktopcontract"
)

// Decode/re-encode with the producer's real types so a removed/renamed field
// fails even when both consumers still accept the old example.
func TestDesktopProducerContract(t *testing.T) {
	for _, c := range desktopcontract.Cases() {
		if c.Accept {
			t.Run(c.Name, func(t *testing.T) {
				var reply desktopResponse
				if err := json.Unmarshal(desktopcontract.Final(c), &reply); err != nil {
					t.Fatal(err)
				}
				if err := desktopcontract.Check(reply, c.Expected); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
