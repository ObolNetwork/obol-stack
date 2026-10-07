package embed

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// frontendSecretEnv are the obol-frontend env vars that carry credentials.
// They must reach the pod via Secret obol-frontend-secrets, never as literal
// Deployment env values (Deployments are readable by the `view` role).
var frontendSecretEnv = []string{"BETTER_AUTH_SECRET", "OBOL_GOOGLE_CLIENT_SECRET"}

// renderHelmfileGotmpl renders a helmfile values .gotmpl with the small
// subset of helmfile/sprig functions these files use. `env` returns a
// recognisable sentinel for every variable so a leak shows up verbatim.
func renderHelmfileGotmpl(t *testing.T, path string) (string, map[string]any) {
	t.Helper()
	raw, err := ReadInfrastructureFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	funcs := template.FuncMap{
		"env": func(name string) string { return "SENTINEL-" + name },
		"default": func(def any, v any) any {
			if s, ok := v.(string); ok && s == "" {
				return def
			}
			if v == nil {
				return def
			}
			return v
		},
		"quote":  func(v any) string { return fmt.Sprintf("%q", fmt.Sprint(v)) },
		"toYaml": func(v any) string { b, _ := yaml.Marshal(v); return strings.TrimSuffix(string(b), "\n") },
		"nindent": func(n int, s string) string {
			pad := strings.Repeat(" ", n)
			return "\n" + pad + strings.ReplaceAll(s, "\n", "\n"+pad)
		},
	}
	tpl, err := template.New(path).Funcs(funcs).Parse(string(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out bytes.Buffer
	data := map[string]any{"Values": map[string]any{"network": "mainnet", "x402": map[string]any{}}}
	if err := tpl.Execute(&out, data); err != nil {
		t.Fatalf("render %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("rendered %s is not YAML: %v\n%s", path, err, out.String())
	}
	return out.String(), doc
}

var sensitiveEnvName = regexp.MustCompile(`(?i)(SECRET|PASSWORD|PRIVATE_KEY|API_KEY|MNEMONIC|_TOKEN$)`)

func TestObolFrontendValues_NoLiteralSecretEnv(t *testing.T) {
	rendered, doc := renderHelmfileGotmpl(t, "values/obol-frontend.yaml.gotmpl")

	for _, name := range frontendSecretEnv {
		if strings.Contains(rendered, "SENTINEL-"+name) {
			t.Errorf("host env %s is rendered literally into the obol-frontend values", name)
		}
	}

	envList, ok := nested(doc, "image", "environment").([]any)
	if !ok {
		t.Fatal("obol-frontend values missing image.environment list")
	}
	refs := map[string]map[string]any{}
	for _, e := range envList {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		if sensitiveEnvName.MatchString(name) {
			if _, literal := entry["value"]; literal {
				t.Errorf("env %s looks sensitive but has a literal value; use valueFrom.secretKeyRef", name)
			}
		}
		if ref, ok := nested(entry, "valueFrom", "secretKeyRef").(map[string]any); ok {
			refs[name] = ref
		}
	}

	for _, name := range frontendSecretEnv {
		ref, ok := refs[name]
		if !ok {
			t.Errorf("env %s must use valueFrom.secretKeyRef", name)
			continue
		}
		if ref["name"] != "obol-frontend-secrets" || ref["key"] != name {
			t.Errorf("env %s secretKeyRef = %v, want obol-frontend-secrets/%s", name, ref, name)
		}
		if ref["optional"] != true {
			t.Errorf("env %s secretKeyRef must be optional (feature off when unset)", name)
		}
	}

	if got := nested(doc, "podAnnotations", "secret.reloader.stakater.com/reload"); got != "obol-frontend-secrets" {
		t.Errorf("podAnnotations reloader = %v, want obol-frontend-secrets (secretKeyRef env needs a pod roll on change)", got)
	}
}

// The base release renders the Secret; it must carry every referenced key,
// be fed from the host env, and obol-frontend must wait for base.
func TestObolFrontendSecret_RenderedByBase(t *testing.T) {
	raw, err := ReadInfrastructureFile("base/templates/obol-frontend-secrets.yaml")
	if err != nil {
		t.Fatalf("read Secret template: %v", err)
	}
	tpl := string(raw)
	for _, want := range []string{"kind: Secret", "name: obol-frontend-secrets", "namespace: obol-frontend"} {
		if !strings.Contains(tpl, want) {
			t.Errorf("Secret template missing %q", want)
		}
	}
	for _, name := range frontendSecretEnv {
		if strings.Count(tpl, "  "+name+":") != 2 {
			t.Errorf("Secret template must render key %s in both fromEnv and carry-over branches", name)
		}
	}

	rendered, doc := renderHelmfileGotmpl(t, "values/base.yaml.gotmpl")
	for _, name := range frontendSecretEnv {
		if !strings.Contains(rendered, "SENTINEL-"+name) {
			t.Errorf("base values do not read host env %s", name)
		}
	}
	if _, ok := nested(doc, "frontend", "secrets", "fromEnv").(bool); !ok {
		t.Error("base values frontend.secrets.fromEnv must render as a bool")
	}

	hf, err := ReadInfrastructureFile("helmfile.yaml")
	if err != nil {
		t.Fatalf("read helmfile.yaml: %v", err)
	}
	var state struct {
		Releases []struct {
			Name  string   `yaml:"name"`
			Needs []string `yaml:"needs"`
		} `yaml:"releases"`
	}
	if err := yaml.Unmarshal(hf, &state); err != nil {
		t.Fatalf("parse helmfile.yaml: %v", err)
	}
	for _, r := range state.Releases {
		if r.Name != "obol-frontend" {
			continue
		}
		for _, n := range r.Needs {
			if n == "kube-system/base" {
				return
			}
		}
		t.Fatalf("obol-frontend needs = %v, must include kube-system/base (Secret before Deployment)", r.Needs)
	}
	t.Fatal("obol-frontend release not found in helmfile.yaml")
}
