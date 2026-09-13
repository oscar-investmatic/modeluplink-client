// Package desktopcontract supplies synthetic wire examples to helper and UI tests.
// It is not imported by the shipping applications.
package desktopcontract

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
)

//go:embed fixtures.json
var fixtures []byte

type Case struct {
	Name     string         `json:"name"`
	Wire     string         `json:"wire"`
	Accept   bool           `json:"accept"`
	Expected map[string]any `json:"expected"`
	Stages   []string       `json:"stages"`
}

func Cases() []Case {
	var cases []Case
	if err := json.Unmarshal(fixtures, &cases); err != nil {
		panic(err)
	}
	return cases
}

// Check tests values after decoding into each application's real wire types.
// Unknown additive fields are allowed; expected fields may never disappear.
func Check(value any, expected map[string]any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var decoded any
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		return err
	}
	return subset(decoded, expected, "reply")
}
func subset(got, want any, path string) error {
	switch want := want.(type) {
	case map[string]any:
		obj, ok := got.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is not an object", path)
		}
		for key, value := range want {
			if err := subset(obj[key], value, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		list, ok := got.([]any)
		if !ok || len(list) != len(want) {
			return fmt.Errorf("%s has different items", path)
		}
		for i, value := range want {
			if err := subset(list[i], value, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	default:
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("%s changed: got %v, want %v", path, got, want)
		}
	}
	return nil
}
func Final(c Case) []byte {
	lines := bytes.Split(bytes.TrimSpace([]byte(c.Wire)), []byte("\n"))
	return lines[len(lines)-1]
}
