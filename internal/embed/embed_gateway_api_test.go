package embed

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestGatewayAPICRDsMatchAppVersion keeps the vendored Gateway API CRDs and
// the chart's appVersion (the value Renovate bumps) in lockstep: a bump
// that skips scripts/update-gateway-api-crds.sh fails here instead of
// shipping the old CRDs under a new version label.
func TestGatewayAPICRDsMatchAppVersion(t *testing.T) {
	chart, err := ReadInfrastructureFile("gateway-api-crds/Chart.yaml")
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}

	m := regexp.MustCompile(`(?m)^appVersion: "(v[0-9]+\.[0-9]+\.[0-9]+)"$`).FindSubmatch(chart)
	if m == nil {
		t.Fatal("gateway-api-crds/Chart.yaml has no appVersion: \"vX.Y.Z\" line")
	}

	want := string(m[1])

	data, err := ReadInfrastructureFile("gateway-api-crds/crds/standard-install.yaml")
	if err != nil {
		t.Fatalf("read vendored CRDs: %v", err)
	}

	names := map[string]bool{}
	dec := yaml.NewDecoder(bytes.NewReader(data))

	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name        string            `yaml:"name"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"metadata"`
			Spec struct {
				Group string `yaml:"group"`
			} `yaml:"spec"`
		}

		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			t.Fatalf("decode vendored CRDs: %v", err)
		}

		if doc.Kind == "" {
			continue
		}

		if doc.Kind != "CustomResourceDefinition" {
			t.Errorf("crds/ must hold CRDs only, found %s %s", doc.Kind, doc.Metadata.Name)
			continue
		}

		if doc.Spec.Group != "gateway.networking.k8s.io" {
			t.Errorf("unexpected CRD group %q (%s)", doc.Spec.Group, doc.Metadata.Name)
		}

		if got := doc.Metadata.Annotations["gateway.networking.k8s.io/bundle-version"]; got != want {
			t.Errorf("%s: bundle-version %q, Chart.yaml appVersion %q (run scripts/update-gateway-api-crds.sh)", doc.Metadata.Name, got, want)
		}

		if got := doc.Metadata.Annotations["gateway.networking.k8s.io/channel"]; got != "standard" {
			t.Errorf("%s: channel %q, want standard", doc.Metadata.Name, got)
		}

		names[doc.Metadata.Name] = true
	}

	// The kinds the stack and its agents create (templates, render.go,
	// hermes.go, tunnel.go) must be served.
	for _, n := range []string{
		"gatewayclasses.gateway.networking.k8s.io",
		"gateways.gateway.networking.k8s.io",
		"httproutes.gateway.networking.k8s.io",
		"referencegrants.gateway.networking.k8s.io",
	} {
		if !names[n] {
			t.Errorf("vendored Gateway API CRDs lack %s", n)
		}
	}
}

// TestInfrastructureHelmfile_GatewayAPICRDsBeforeTraefik: the Traefik chart
// renders the Gateway and GatewayClass, so on a fresh install the Gateway
// API CRDs must be installed first (Traefik chart 40.2+ no longer ships
// them).
func TestInfrastructureHelmfile_GatewayAPICRDsBeforeTraefik(t *testing.T) {
	data, err := ReadInfrastructureFile("helmfile.yaml")
	if err != nil {
		t.Fatalf("read helmfile.yaml: %v", err)
	}

	var hf struct {
		Releases []struct {
			Name      string   `yaml:"name"`
			Namespace string   `yaml:"namespace"`
			Chart     string   `yaml:"chart"`
			Needs     []string `yaml:"needs"`
		} `yaml:"releases"`
	}
	if err := yaml.Unmarshal(data, &hf); err != nil {
		t.Fatalf("parse helmfile.yaml: %v", err)
	}

	crdsIdx, traefikIdx := -1, -1

	for i, r := range hf.Releases {
		switch r.Name {
		case "gateway-api-crds":
			crdsIdx = i

			if r.Chart != "./gateway-api-crds" || r.Namespace != "kube-system" {
				t.Errorf("gateway-api-crds release: chart %q namespace %q", r.Chart, r.Namespace)
			}
		case "traefik":
			traefikIdx = i

			found := false

			for _, n := range r.Needs {
				if n == "kube-system/gateway-api-crds" {
					found = true
				}
			}

			if !found {
				t.Errorf("traefik release must need kube-system/gateway-api-crds, needs=%v", r.Needs)
			}
		}
	}

	if crdsIdx < 0 || traefikIdx < 0 {
		t.Fatalf("releases missing: gateway-api-crds=%d traefik=%d", crdsIdx, traefikIdx)
	}

	if crdsIdx > traefikIdx {
		t.Errorf("gateway-api-crds (#%d) must be declared before traefik (#%d)", crdsIdx, traefikIdx)
	}
}
