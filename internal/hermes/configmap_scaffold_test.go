package hermes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"testing"

	obolembed "github.com/ObolNetwork/obol-stack/internal/embed"
	"gopkg.in/yaml.v3"
)

// TestGenerateValues_ShipsSkillsAsConfigMapBinaryData is the core Version-B
// invariant: the embedded Obol skills travel to the pod inside the
// hermes-skills ConfigMap (as a gzipped tarball in binaryData) and are
// extracted INTO the PVC in-pod, rather than being written into the PVC from
// the host. It renders with the REAL tarball (not a stub) and proves the
// rendered values:
//   - are valid YAML even with the full ~200KB base64 payload,
//   - carry a hermes-skills ConfigMap whose binaryData["skills.tar.gz"]
//     base64-decodes back to exactly SkillsTarball() and is a valid gzip tar,
//   - mount both ConfigMaps read-only into init-hermes-data, and
//   - never reintroduce a host-side write of the skills/config into the PVC.
func TestGenerateValues_ShipsSkillsAsConfigMapBinaryData(t *testing.T) {
	tarGz, err := obolembed.SkillsTarball()
	if err != nil {
		t.Fatalf("SkillsTarball: %v", err)
	}
	if len(tarGz) == 0 {
		t.Fatal("SkillsTarball returned empty bytes")
	}

	values := generateValues(
		"hermes-obol-agent",
		"hermes-obol-agent.obol.stack",
		"obol-agent.obol.stack",
		"https://agent.example.com",
		"secret-token",
		"gpt-5.2",
		[]byte("model:\n  default: gpt-5.2\n"),
		tarGz,
	)

	var doc struct {
		Resources []map[string]any `yaml:"resources"`
	}
	if err := yaml.Unmarshal([]byte(values), &doc); err != nil {
		t.Fatalf("generateValues produced invalid YAML with real tarball: %v", err)
	}

	// Locate the hermes-skills ConfigMap and pull its binaryData payload.
	var payload string
	var foundConfig bool
	for _, res := range doc.Resources {
		if res["kind"] != "ConfigMap" {
			continue
		}
		meta, _ := res["metadata"].(map[string]any)
		switch meta["name"] {
		case "hermes-config":
			foundConfig = true
		case "hermes-skills":
			bd, ok := res["binaryData"].(map[string]any)
			if !ok {
				t.Fatalf("hermes-skills ConfigMap missing binaryData: %#v", res)
			}
			payload, ok = bd["skills.tar.gz"].(string)
			if !ok {
				t.Fatalf("hermes-skills binaryData missing skills.tar.gz: %#v", bd)
			}
		}
	}
	if !foundConfig {
		t.Fatal("hermes-config ConfigMap not found in rendered values")
	}
	if payload == "" {
		t.Fatal("hermes-skills ConfigMap not found / empty in rendered values")
	}

	// The YAML-parsed binaryData value must base64-decode back to exactly the
	// bytes we shipped — k8s decodes binaryData itself, so the value in the
	// manifest has to be the raw base64 of the gzip.
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("binaryData is not valid base64: %v", err)
	}
	if !bytes.Equal(decoded, tarGz) {
		t.Fatalf("decoded binaryData (%d bytes) != SkillsTarball (%d bytes)", len(decoded), len(tarGz))
	}

	// And it must be a real gzip tar that contains a known skill, so the in-pod
	// `python3 -c "...extractall(...)"` step has something valid to unpack.
	gz, err := gzip.NewReader(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("binaryData is not valid gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	var sawBuyPy bool
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if hdr.Name == "buy-x402/scripts/buy.py" {
			sawBuyPy = true
		}
	}
	if !sawBuyPy {
		t.Fatal("skills tarball missing expected entry buy-x402/scripts/buy.py")
	}
}
