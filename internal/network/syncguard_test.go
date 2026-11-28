package network

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// Every client the install flags offer must have a pin the sync guard can
// read, or template drift would silently disable the override warning.
func TestPinnedImageTags_EveryClient(t *testing.T) {
	helmfile, err := embed.ReadEmbeddedNetworkFile("ethereum", "helmfile.yaml.gotmpl")
	if err != nil {
		t.Fatal(err)
	}

	values, err := embed.ReadEmbeddedNetworkFile("ethereum", "values.yaml.gotmpl")
	if err != nil {
		t.Fatal(err)
	}

	pins := pinnedImageTags(helmfile)

	enum := regexp.MustCompile(`(?m)^# @enum (.+)\n# @default \S+\n# @description (Execution|Consensus) layer client`)
	matches := enum.FindAllStringSubmatch(string(values), -1)

	if len(matches) != 2 {
		t.Fatalf("found %d client enums in values.yaml.gotmpl, want 2", len(matches))
	}

	for _, m := range matches {
		for client := range strings.SplitSeq(m[1], ",") {
			if pins[client] == "" {
				t.Errorf("no image tag pin found for %s", client)
			}
		}
	}

	if got := pins["silver"]; !strings.HasPrefix(got, "v0.1.0-alpha.") {
		t.Errorf("silver pin = %q", got)
	}
}

func TestImageTag(t *testing.T) {
	for image, want := range map[string]string{
		"ghcr.io/gattaca-com/silver:v0.1.0-alpha.23":                 "v0.1.0-alpha.23",
		"ghcr.io/gattaca-com/silver:v0.1.0-alpha.23@sha256:712131bd": "v0.1.0-alpha.23",
		"localhost:5000/reth":                                        "",
		"localhost:5000/reth:v2.11.0":                                "v2.11.0",
		"sigp/lighthouse":                                            "",
	} {
		if got := imageTag(image); got != want {
			t.Errorf("imageTag(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestPlanSyncImages(t *testing.T) {
	d := Deployment{Type: TypeEthereum, ID: "hoodi", Namespace: "ethereum-hoodi", Network: "hoodi", ExecutionClient: "reth", ConsensusClient: "silver"}
	oldPins := map[string]string{"reth": "v2.10.1", "silver": "v0.1.0-alpha.13", "lighthouse": "v8.3.3"}
	newPins := map[string]string{"reth": "v2.11.0", "silver": "v0.1.0-alpha.23", "lighthouse": "v8.3.3"}

	cases := []struct {
		name      string
		live      map[string]string
		overrides map[string]string
		drift     []string
		resync    bool
	}{
		{name: "plain upgrade from the old pins", live: map[string]string{"reth": "ghcr.io/paradigmxyz/reth:v2.10.1", "silver": "ghcr.io/gattaca-com/silver:v0.1.0-alpha.13"}, resync: true},
		{name: "already on the new pins", live: map[string]string{"reth": "ghcr.io/paradigmxyz/reth:v2.11.0", "silver": "ghcr.io/gattaca-com/silver:v0.1.0-alpha.23@sha256:abc"}},
		{name: "hand-set image", live: map[string]string{"reth": "ghcr.io/paradigmxyz/reth:v2.10.0", "silver": "ghcr.io/gattaca-com/silver:v0.1.0-alpha.22"}, drift: []string{"reth", "silver"}},
		{name: "override keeps a hand-set image", live: map[string]string{"silver": "ghcr.io/gattaca-com/silver:v0.1.0-alpha.22"}, overrides: map[string]string{"silver": "v0.1.0-alpha.22"}},
		{name: "override to an old silver: no resync", live: map[string]string{"silver": "ghcr.io/gattaca-com/silver:v0.1.0-alpha.13"}, overrides: map[string]string{"silver": "v0.1.0-alpha.13"}},
		{name: "silver from alpha.17 to alpha.18", live: map[string]string{"silver": "ghcr.io/gattaca-com/silver:v0.1.0-alpha.17"}, overrides: map[string]string{"silver": "v0.1.0-alpha.18"}, drift: []string{"silver"}, resync: true},
		{name: "client switched: not an override", live: map[string]string{"lighthouse": "sigp/lighthouse:v8.3.3"}},
		{name: "nothing live", live: map[string]string{}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			overrides := c.overrides
			if overrides == nil {
				overrides = map[string]string{}
			}

			plan := planSyncImages(d, c.live, oldPins, newPins, overrides)

			var drift []string
			for _, dr := range plan.Drift {
				drift = append(drift, dr.Client)
			}

			if strings.Join(drift, ",") != strings.Join(c.drift, ",") {
				t.Errorf("drift = %v, want %v", drift, c.drift)
			}

			if plan.SilverResync != c.resync {
				t.Errorf("silver resync = %v, want %v", plan.SilverResync, c.resync)
			}
		})
	}
}

func TestOverrideImageTags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overrides.yaml")

	if tags, err := overrideImageTags(path); err != nil || len(tags) != 0 {
		t.Fatalf("missing file = (%v, %v)", tags, err)
	}

	if err := os.WriteFile(path, []byte("silver:\n  image:\n    tag: v0.1.0-alpha.22\nnimbus:\n  image:\n    tag: multiarch-v26.10.2\nreth:\n  extraArgs: [--foo]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tags, err := overrideImageTags(path)
	if err != nil {
		t.Fatal(err)
	}

	if tags["silver"] != "v0.1.0-alpha.22" || tags["nimbus"] != "multiarch-v26.10.2" || len(tags) != 2 {
		t.Fatalf("tags = %v", tags)
	}
}

// Moving Silver across its data-format change needs a confirmation; without
// a terminal sync refuses and prints the steps, and --yes proceeds. Nothing
// is deleted either way.
func TestGuardSyncImages_SilverResync(t *testing.T) {
	orig := liveImagesFn
	t.Cleanup(func() { liveImagesFn = orig })

	liveImagesFn = func(*config.Config, string) (map[string]string, error) {
		return map[string]string{"silver": "ghcr.io/gattaca-com/silver:v0.1.0-alpha.13", "reth": "ghcr.io/paradigmxyz/reth:v2.10.1"}, nil
	}

	cfg := &config.Config{DataDir: "/data"}
	d := Deployment{Type: TypeEthereum, ID: "hoodi", Namespace: "ethereum-hoodi", Network: "hoodi", ExecutionClient: "reth", ConsensusClient: "silver"}
	oldHelmfile := []byte(`{{- if eq .Values.consensusClient "silver" }}
            tag: v0.1.0-alpha.13
{{- else if eq .Values.executionClient "reth" }}
            tag: v2.10.1
`)
	newHelmfile, _ := embed.ReadEmbeddedNetworkFile("ethereum", "helmfile.yaml.gotmpl")

	var out bytes.Buffer

	err := guardSyncImages(cfg, ui.NewForTest(&out, &out), d, t.TempDir(), oldHelmfile, newHelmfile, false)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("non-interactive sync without --yes = %v, want a refusal naming --yes", err)
	}

	for _, want := range []string{
		"obol network stop ethereum/hoodi",
		"obol network sync ethereum/hoodi --yes",
		filepath.Join("/data", "ethereum-hoodi", "consensus-silver-hoodi"),
		"obol network start ethereum/hoodi",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%v", want, err)
		}
	}

	// On a terminal the question defaults to No (empty answer).
	t.Setenv("OBOL_NONINTERACTIVE", "")

	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}

	origStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = origStdin })

	err = guardSyncImages(cfg, ui.NewForTestTTY(&out, &out), d, t.TempDir(), oldHelmfile, newHelmfile, false)
	if err == nil || !strings.Contains(err.Error(), "cancelled; nothing changed") {
		t.Fatalf("TTY with an empty answer = %v, want cancelled", err)
	}

	if err := guardSyncImages(cfg, ui.NewForTest(&out, &out), d, t.TempDir(), oldHelmfile, newHelmfile, true); err != nil {
		t.Fatalf("--yes: %v", err)
	}

	// No cluster: nothing to compare, sync goes ahead.
	liveImagesFn = func(*config.Config, string) (map[string]string, error) { return nil, errors.New("no cluster") }
	if err := guardSyncImages(cfg, ui.NewForTest(&out, &out), d, t.TempDir(), oldHelmfile, newHelmfile, false); err != nil {
		t.Fatalf("no cluster: %v", err)
	}
}

func TestParseStatefulSetImages(t *testing.T) {
	raw := []byte(`{"items":[
	  {"spec":{"template":{"spec":{"containers":[{"name":"silver","image":"ghcr.io/gattaca-com/silver:v0.1.0-alpha.23"}]}}}},
	  {"spec":{"template":{"spec":{"containers":[{"name":"reth","image":"ghcr.io/paradigmxyz/reth:v2.11.0"}]}}}}]}`)

	images, err := parseStatefulSetImages(raw)
	if err != nil || images["silver"] != "ghcr.io/gattaca-com/silver:v0.1.0-alpha.23" || images["reth"] != "ghcr.io/paradigmxyz/reth:v2.11.0" {
		t.Fatalf("images = %v (%v)", images, err)
	}
}
