package images

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every managed image must be published per commit and checked by the
// release gate, or a release can pin a :<short-sha> that does not exist.
func TestManagedMatchesReleaseGateImages(t *testing.T) {
	raw, err := os.ReadFile("../../.github/scripts/lib-ghcr.sh")
	if err != nil {
		t.Fatal(err)
	}

	var gate []string

	arrays := regexp.MustCompile(`(?ms)^[A-Z0-9_]+_IMAGES=\((.*?)\)`)
	for _, m := range arrays.FindAllStringSubmatch(string(raw), -1) {
		gate = append(gate, strings.Fields(m[1])...)
	}

	var managed []string
	for _, repo := range Managed {
		managed = append(managed, strings.TrimPrefix(repo, "ghcr.io/obolnetwork/"))
	}

	sort.Strings(gate)
	sort.Strings(managed)

	if strings.Join(gate, ",") != strings.Join(managed, ",") {
		t.Fatalf("lib-ghcr.sh image groups %v != images.Managed %v", gate, managed)
	}
}
