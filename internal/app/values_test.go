package app

import (
	"os"
	"path/filepath"
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
