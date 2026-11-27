package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}

func readValues(t *testing.T, path string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read values: %v", err)
	}

	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		t.Fatalf("parse values: %v", err)
	}

	return values
}

func TestApplyOverridesToFile_SetExpressions(t *testing.T) {
	dir := t.TempDir()
	valuesPath := writeFile(t, dir, "values.yaml", "image:\n  tag: latest\nreplicas: 1\n")

	err := applyOverridesToFile(valuesPath, nil, []string{
		"image.tag=1.2.3",   // overwrite nested scalar
		"service.port=8080", // create intermediate map
		"persistence=true",  // typed bool
		"replicas=3",        // typed int
		"name=my-app",       // plain string
	})
	if err != nil {
		t.Fatalf("applyOverridesToFile: %v", err)
	}

	values := readValues(t, valuesPath)

	if got := values["image"].(map[string]any)["tag"]; got != "1.2.3" {
		t.Errorf("image.tag = %v, want 1.2.3", got)
	}
	if got := values["service"].(map[string]any)["port"]; got != 8080 {
		t.Errorf("service.port = %v (%T), want int 8080", got, got)
	}
	if got := values["persistence"]; got != true {
		t.Errorf("persistence = %v (%T), want bool true", got, got)
	}
	if got := values["replicas"]; got != 3 {
		t.Errorf("replicas = %v (%T), want int 3", got, got)
	}
	if got := values["name"]; got != "my-app" {
		t.Errorf("name = %v, want my-app", got)
	}
}

func TestApplyOverridesToFile_ValuesFilesMergeInOrder(t *testing.T) {
	dir := t.TempDir()
	valuesPath := writeFile(t, dir, "values.yaml", "image:\n  tag: latest\n  pullPolicy: IfNotPresent\n")
	first := writeFile(t, dir, "first.yaml", "image:\n  tag: from-first\nextra: 1\n")
	second := writeFile(t, dir, "second.yaml", "image:\n  tag: from-second\n")

	if err := applyOverridesToFile(valuesPath, []string{first, second}, []string{"extra=2"}); err != nil {
		t.Fatalf("applyOverridesToFile: %v", err)
	}

	values := readValues(t, valuesPath)
	image := values["image"].(map[string]any)

	if image["tag"] != "from-second" {
		t.Errorf("image.tag = %v, want from-second (later file wins)", image["tag"])
	}
	if image["pullPolicy"] != "IfNotPresent" {
		t.Errorf("image.pullPolicy = %v, want IfNotPresent (untouched keys survive merge)", image["pullPolicy"])
	}
	if values["extra"] != 2 {
		t.Errorf("extra = %v, want 2 (--set applies after --values)", values["extra"])
	}
}

func TestApplyOverridesToFile_CommentOnlyValues(t *testing.T) {
	dir := t.TempDir()
	valuesPath := writeFile(t, dir, "values.yaml", "# No default values in chart\n")

	if err := applyOverridesToFile(valuesPath, nil, []string{"a.b=c"}); err != nil {
		t.Fatalf("applyOverridesToFile on comment-only file: %v", err)
	}

	values := readValues(t, valuesPath)
	if got := values["a"].(map[string]any)["b"]; got != "c" {
		t.Errorf("a.b = %v, want c", got)
	}
}

func TestApplyOverridesToFile_InvalidSetExpression(t *testing.T) {
	dir := t.TempDir()
	valuesPath := writeFile(t, dir, "values.yaml", "a: 1\n")

	if err := applyOverridesToFile(valuesPath, nil, []string{"no-equals-sign"}); err == nil {
		t.Error("expected error for --set expression without '='")
	}
	if err := applyOverridesToFile(valuesPath, nil, []string{"=value"}); err == nil {
		t.Error("expected error for --set expression with empty key")
	}
}

const (
	testAddress = "0x3D1fD3D27b1C6Aa4Bd2fBc4fDaF8B53e70a7dbe2"
	testHash    = "0x7f0f4b3c1a2d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7"
)

// Regression: re-serializing values dropped the quotes on hex strings, and
// helm then read an address as the float 3.49e+47.
func TestApplyOverridesToFile_KeepsHexStringsQuoted(t *testing.T) {
	dir := t.TempDir()
	valuesPath := writeFile(t, dir, "values.yaml",
		"# operator settings\n"+
			"tests:\n"+
			"  dkgSidecar:\n"+
			"    operatorAddress: \""+testAddress+"\"\n"+
			"    configHash: \""+testHash+"\" # target hash\n"+
			"zeta: 1\n"+
			"alpha: 2\n")

	err := applyOverridesToFile(valuesPath, nil, []string{
		"charon.operatorAddress=" + testAddress,
		"dkg.definition.configHash=" + testHash,
		"small=0x10",
	})
	if err != nil {
		t.Fatalf("applyOverridesToFile: %v", err)
	}

	data, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatal(err)
	}

	out := string(data)

	for _, want := range []string{
		`operatorAddress: "` + testAddress + `"`,
		`configHash: "` + testHash + `"`,
		`small: "0x10"`,
		"# operator settings",
		"# target hash",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("values.yaml missing %q:\n%s", want, out)
		}
	}

	if strings.Index(out, "zeta") > strings.Index(out, "alpha") {
		t.Errorf("key order not preserved (zeta before alpha):\n%s", out)
	}

	// Values set as hex stay strings when read back.
	values := readValues(t, valuesPath)
	if got := values["charon"].(map[string]any)["operatorAddress"]; got != testAddress {
		t.Errorf("charon.operatorAddress = %v (%T)", got, got)
	}
}

// A bare hex value already in a file (written by an older obol) gets
// quoted on the next write instead of staying a float for helm.
func TestApplyOverridesToFile_QuotesExistingBareHex(t *testing.T) {
	dir := t.TempDir()
	valuesPath := writeFile(t, dir, "values.yaml", "addr: "+testAddress+"\nversion: 1.2.3\n")

	if err := applyOverridesToFile(valuesPath, nil, []string{"x=1"}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(valuesPath)
	if !strings.Contains(string(data), `addr: "`+testAddress+`"`) {
		t.Errorf("bare hex not quoted:\n%s", data)
	}

	if !strings.Contains(string(data), `version: "1.2.3"`) {
		t.Errorf("number-like string not quoted:\n%s", data)
	}
}

func TestBareHexPaths(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "values.yaml",
		"tests:\n  dkgSidecar:\n    a: "+testAddress+"\n    b: \""+testHash+"\"\nlist:\n  - "+testHash+"\nsmall: 0x10\n")

	got, err := bareHexPaths(path)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"tests.dkgSidecar.a", "list[0]"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("bareHexPaths = %v, want %v", got, want)
	}
}
