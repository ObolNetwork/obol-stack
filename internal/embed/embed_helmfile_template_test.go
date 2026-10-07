package embed

import (
	"strings"
	"testing"
)

// TestInfrastructureHelmfile_NoInlineTemplates guards the defaults helmfile
// against Go-template expressions. Helmfile v1 does not render a plain
// helmfile.yaml (only *.gotmpl files): a quoted `"{{ .Values.network }}"`
// silently reached the base chart as that literal string, and an unquoted
// `{{- toYaml ... }}` block made `obol stack up` fail to parse the file.
// Put templated values in a ./values/*.yaml.gotmpl file instead.
func TestInfrastructureHelmfile_NoInlineTemplates(t *testing.T) {
	data, err := ReadInfrastructureFile("helmfile.yaml")
	if err != nil {
		t.Fatalf("read embedded helmfile.yaml: %v", err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		code := line
		if idx := strings.Index(code, "#"); idx >= 0 {
			code = code[:idx]
		}
		if strings.Contains(code, "{{") {
			t.Errorf("helmfile.yaml:%d has an inline template (not rendered by helmfile v1): %s", i+1, strings.TrimSpace(line))
		}
	}
}
