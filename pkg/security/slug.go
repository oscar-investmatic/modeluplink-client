package security

import (
	"errors"
	"regexp"
	"strings"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,46}[a-z0-9]$`)

var reservedSlugs = map[string]struct{}{
	"api": {}, "app": {}, "www": {}, "admin": {}, "billing": {}, "control": {},
	"relay": {}, "eu-relay": {}, "us-relay": {}, "ap-relay": {}, "status": {},
	"support": {}, "security": {}, "mail": {}, "smtp": {}, "staging": {}, "dev": {},
}

func ValidateSlug(slug string) error {
	if slug != strings.ToLower(slug) || !slugPattern.MatchString(slug) {
		return errors.New("endpoint name must be 3-48 lowercase letters, digits, or single hyphen-separated words")
	}
	if strings.Contains(slug, "--") {
		return errors.New("endpoint name cannot contain consecutive hyphens")
	}
	if _, reserved := reservedSlugs[slug]; reserved {
		return errors.New("endpoint name is reserved")
	}
	return nil
}
