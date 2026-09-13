// Package buildinfo identifies the source used for a client build.
package buildinfo

import (
	"regexp"
	"runtime/debug"
)

const Repository = "https://github.com/oscar-investmatic/modeluplink-client"

// Revision is injected only after the packaging script verifies a clean checkout.
var Revision string
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type Info struct {
	Revision  string `json:"revision"`
	Modified  bool   `json:"modified"`
	SourceURL string `json:"source_url"`
}

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
