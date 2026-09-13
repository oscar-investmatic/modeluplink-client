package security

import (
	"errors"
	"net/url"
	"strings"
)

// NormalizeCORSOrigin accepts only exact HTTP(S) origins. Paths, credentials,
// wildcards, queries, and fragments are intentionally unsupported.
func NormalizeCORSOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", errors.New("CORS origin must be an absolute http or https origin")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("CORS origin must use http or https")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("CORS origin cannot contain credentials, a path, query, or fragment")
	}
	if strings.Contains(u.Host, "*") {
		return "", errors.New("CORS origin wildcards are not supported")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}
