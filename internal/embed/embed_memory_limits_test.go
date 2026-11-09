package embed

import (
	"bytes"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	templateOnlyLine = regexp.MustCompile(`(?m)^\s*\{\{-?[^}]*-?\}\}\s*$\n?`)
	templateAction   = regexp.MustCompile(`\{\{[^}]*\}\}`)
	workloadKind     = regexp.MustCompile(`(?m)^kind:\s*(Deployment|StatefulSet|DaemonSet)\s*$`)
)

// TestInfrastructureWorkloadsHaveMemoryLimits: every container of every
// workload in the base chart sets a memory request and limit, so no stack
// pod can grow until the k3d node (or the host) runs out. Chart-based
// components (monitoring, traefik, reloader, eRPC, frontend, cloudflared)
// set theirs in values/ and helmfile.yaml.
func TestInfrastructureWorkloadsHaveMemoryLimits(t *testing.T) {
	checked := 0

	err := fs.WalkDir(infrastructureFS, "infrastructure/base/templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}

		raw, err := fs.ReadFile(infrastructureFS, path)
		if err != nil || !workloadKind.Match(raw) {
			return err
		}

		// Drop lines that are only template actions ({{- if }}, {{- end }}),
		// and stand in for inline ones, so the manifests parse as YAML.
		src := templateAction.ReplaceAll(templateOnlyLine.ReplaceAll(raw, nil), []byte("x"))

		dec := yaml.NewDecoder(bytes.NewReader(src))
		for {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
				Spec struct {
					Template struct {
						Spec struct {
							InitContainers []container `yaml:"initContainers"`
							Containers     []container `yaml:"containers"`
						} `yaml:"spec"`
					} `yaml:"template"`
				} `yaml:"spec"`
			}
			if err := dec.Decode(&doc); err != nil {
				if err.Error() == "EOF" {
					return nil
				}
				t.Fatalf("%s: %v", path, err)
			}

			switch doc.Kind {
			case "Deployment", "StatefulSet", "DaemonSet":
			default:
				continue
			}

			pod := doc.Spec.Template.Spec
			for _, c := range append(pod.InitContainers, pod.Containers...) {
				if c.Resources.Requests.Memory == "" || c.Resources.Limits.Memory == "" {
					t.Errorf("%s: %s %s container %s: memory request %q, limit %q; want both",
						path, doc.Kind, doc.Metadata.Name, c.Name, c.Resources.Requests.Memory, c.Resources.Limits.Memory)
				}
				checked++
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	if checked < 6 {
		t.Errorf("checked only %d containers; did the template parsing break?", checked)
	}
}

type container struct {
	Name      string `yaml:"name"`
	Resources struct {
		Requests struct {
			Memory string `yaml:"memory"`
		} `yaml:"requests"`
		Limits struct {
			Memory string `yaml:"memory"`
		} `yaml:"limits"`
	} `yaml:"resources"`
}
