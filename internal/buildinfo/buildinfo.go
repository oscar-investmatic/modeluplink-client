// Package buildinfo identifies the source used for a client build.
package buildinfo

import (
	"regexp"
	"runtime/debug"
)

const Repository = "https://github.com/oscar-investmatic/modeluplink-client"

// Revision is injected by packaging after checking the source checkout.
// It is build metadata, not a signature; attestations establish the build origin.
var Revision string
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

// Info describes the source metadata embedded in this executable.
type Info struct {
	Revision  string `json:"revision"`
	Modified  bool   `json:"modified"`
	SourceURL string `json:"source_url"`
}

// Current prefers the injected revision and otherwise reads Go VCS metadata.
// Unknown or modified builds link to the repository rather than an exact source tree.
func Current() Info {
	result := Info{Revision: Revision, Modified: true, SourceURL: Repository}
	if Revision != "" {
		result.Modified = false
	} else if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				result.Revision = setting.Value
			case "vcs.modified":
				result.Modified = setting.Value != "false"
			}
		}
	}
	if !revisionPattern.MatchString(result.Revision) {
		result.Revision = "unknown"
		result.Modified = true
	}
	if !result.Modified {
		result.SourceURL += "/tree/" + result.Revision
	}
	return result
}
