package naming

import (
	"regexp"
	"testing"

	"github.com/oscar-investmatic/modeluplink-client/pkg/security"
)

func TestTrialSlugIsMemorableAndValid(t *testing.T) {
	pattern := regexp.MustCompile(`^try-[a-z]+-[a-z]+-[0-9]{2,3}$`)
	for range 100 {
		slug, err := NewTrialSlug()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(slug) {
			t.Fatalf("trial slug %q is not a mission call sign", slug)
		}
		if err = security.ValidateSlug(slug); err != nil {
			t.Fatalf("trial slug %q is invalid: %v", slug, err)
		}
	}
}

func TestPaidSuggestionsAreStableDistinctAndValid(t *testing.T) {
	first, again := PaidSuggestions("acct_test"), PaidSuggestions("acct_test")
	if len(first) != 3 || len(again) != 3 {
		t.Fatalf("suggestions=%v", first)
	}
	seen := map[string]bool{}
	for i, suggestion := range first {
		if suggestion != again[i] || seen[suggestion] {
			t.Fatalf("suggestions are unstable or repeated: %v / %v", first, again)
		}
		seen[suggestion] = true
		if err := security.ValidateSlug(suggestion); err != nil {
			t.Fatalf("suggestion %q is invalid: %v", suggestion, err)
		}
	}
}

func TestNormalizeMissionName(t *testing.T) {
	for input, want := range map[string]string{
		" Atlas Lab ":       "atlas-lab",
		"MY__Home...ROVER":  "my-home-rover",
		"---Quiet Orbit---": "quiet-orbit",
	} {
		if got := NormalizeMissionName(input); got != want {
			t.Fatalf("NormalizeMissionName(%q)=%q, want %q", input, got, want)
		}
	}
}
