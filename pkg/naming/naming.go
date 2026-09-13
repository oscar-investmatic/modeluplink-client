// Package naming creates memorable, non-identifying endpoint call signs.
package naming

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

var adjectives = []string{
	"amber", "bold", "brave", "bright", "calm", "cheerful", "clever", "coral",
	"cosmic", "crimson", "crystal", "daring", "eager", "electric", "flying", "fuzzy",
	"gentle", "glowing", "golden", "grand", "green", "happy", "hidden", "icy",
	"jolly", "kind", "lively", "lucky", "lunar", "magic", "mellow", "merry",
	"mighty", "neon", "nimble", "noble", "orange", "peaceful", "playful", "polar",
	"quiet", "radiant", "rapid", "red", "ruby", "silver", "solar", "soft",
	"sparkly", "speedy", "steady", "stellar", "sunny", "swift", "tiny", "velvet",
	"violet", "vivid", "wandering", "warm", "white", "wild", "wise", "zippy",
}

var creatures = []string{
	"alpaca", "badger", "bear", "beaver", "bison", "bobcat", "capybara", "cheetah",
	"dolphin", "dragon", "eagle", "falcon", "ferret", "finch", "fox", "gecko",
	"gibbon", "giraffe", "goose", "hamster", "hare", "hedgehog", "heron", "ibis",
	"jaguar", "koala", "lemur", "leopard", "lion", "llama", "lynx", "magpie",
	"marmot", "moose", "narwhal", "ocelot", "orca", "otter", "owl", "panda",
	"panther", "parrot", "penguin", "phoenix", "puffin", "rabbit", "raccoon", "raven",
	"robin", "seal", "sparrow", "squid", "starling", "stoat", "swan", "tiger",
	"toucan", "turtle", "walrus", "whale", "wolf", "wombat", "yak", "zebra",
}

var missionNouns = []string{
	"apollo", "atlas", "aurora", "beacon", "capsule", "comet", "cosmos", "eclipse",
	"galaxy", "harbor", "horizon", "lander", "launch", "meteor", "mission", "moonbeam",
	"nebula", "nova", "odyssey", "orbit", "outpost", "pathfinder", "photon", "pioneer",
	"pulsar", "quasar", "ranger", "rocket", "rover", "satellite", "shuttle", "signal",
	"skyline", "star", "starship", "station", "sunbeam", "telescope", "trailblazer", "vector",
	"venture", "voyager", "zenith", "airlock", "asteroid", "cosmonaut", "crater", "discovery",
	"gravity", "infinity", "jetstream", "lightyear", "module", "northstar", "observatory", "orbiter",
	"payload", "radiowave", "solstice", "stardust", "sunrise", "transit", "twilight", "wayfinder",
}

// NewTrialSlug returns a temporary mission call sign such as
// try-brave-otter-27. The number is two or three digits; together with the two
// word lists it gives the pilot millions of possibilities, and provisioning
// still retries the authoritative global uniqueness check.
func NewTrialSlug() (string, error) {
	adjective, err := randomIndex(len(adjectives))
	if err != nil {
		return "", err
	}
	creature, err := randomIndex(len(creatures))
	if err != nil {
		return "", err
	}
	number, err := randomIndex(990)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("try-%s-%s-%d", adjectives[adjective], creatures[creature], number+10), nil
}

// PaidSuggestions returns stable mission-name ideas for an account. Stability
// keeps quiet desktop refreshes from replacing a name while somebody types;
// the seed is never included in a suggestion or sent to logs.
func PaidSuggestions(seed string) []string {
	out := make([]string, 0, 3)
	for i := 0; len(out) < 3; i++ {
		sum := sha256.Sum256([]byte("modeluplink-paid-name\x00" + seed + "\x00" + strconv.Itoa(i)))
		adjective := adjectives[int(binary.BigEndian.Uint16(sum[0:2]))%len(adjectives)]
		noun := missionNouns[int(binary.BigEndian.Uint16(sum[2:4]))%len(missionNouns)]
		candidate := adjective + "-" + noun
		if !contains(out, candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

// NewPaidName is the CLI fallback when no permanent --name is supplied.
func NewPaidName() (string, error) {
	adjective, err := randomIndex(len(adjectives))
	if err != nil {
		return "", err
	}
	noun, err := randomIndex(len(missionNouns))
	if err != nil {
		return "", err
	}
	return adjectives[adjective] + "-" + missionNouns[noun], nil
}

// NormalizeMissionName turns ordinary typed text into the paid slug format.
// Validation remains authoritative in the security package and control API.
func NormalizeMissionName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	dash := false
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if valid {
			if dash && out.Len() > 0 && out.Len() < 48 {
				out.WriteByte('-')
			}
			dash = false
			if out.Len() < 48 {
				out.WriteRune(r)
			}
		} else {
			dash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func randomIndex(size int) (int, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(int64(size)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
