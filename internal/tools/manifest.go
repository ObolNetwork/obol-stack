// Package tools manages the host binaries the obol CLI shells out to
// (kubectl, helm, k3d, helmfile, k9s) plus the helm-diff plugin.
//
// It provides three things:
//
//   - an embedded, checksum-pinned manifest (manifest.yaml) of the versions
//     obol expects, kept in lockstep with obolup.sh;
//   - a resolver (Path / Resolve) that picks the binary to run in the order
//     OBOL_<TOOL> env override → obol-managed bin dir → compatible $PATH copy;
//   - an installer that downloads, verifies sha256 (fail closed), extracts and
//     atomically installs managed copies into the obol bin dir.
//
// This is what lets an `obol` installed by a package manager (Homebrew cask,
// deb/rpm) bootstrap its own toolchain without obolup.sh.
package tools

import (
	_ "embed"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed manifest.yaml
var manifestYAML []byte

// Compat policies for accepting a $PATH binary instead of a managed copy.
const (
	CompatSkew1 = "skew1" // same major, minor within ±1 of the pin
	CompatMinor = "minor" // same major, minor >= the pin's minor
	CompatAny   = "any"   // any version
)

// Tool kinds.
const (
	KindBinary     = "binary"
	KindHelmPlugin = "helm-plugin"
)

// Archive formats.
const (
	FormatRaw   = "raw"
	FormatTarGz = "tar.gz"
	FormatZip   = "zip"
)

// Asset is one downloadable artifact for a single os/arch.
type Asset struct {
	URL    string `yaml:"url"`
	SHA256 string `yaml:"sha256"`
	Member string `yaml:"member,omitempty"`
}

// Tool is one manifest entry.
type Tool struct {
	Name         string           `yaml:"name"`
	Version      string           `yaml:"version"`
	ObolupVar    string           `yaml:"obolupVar"`
	Required     bool             `yaml:"required"`
	Kind         string           `yaml:"kind,omitempty"`
	PluginName   string           `yaml:"pluginName,omitempty"`
	Compat       string           `yaml:"compat,omitempty"`
	VersionArgs  []string         `yaml:"versionArgs,omitempty"`
	VersionRegex string           `yaml:"versionRegex,omitempty"`
	Format       string           `yaml:"format"`
	Assets       map[string]Asset `yaml:"assets"`
}

// Manifest is the parsed manifest.yaml.
type Manifest struct {
	Tools []Tool `yaml:"tools"`
}

// IsPlugin reports whether the tool is a helm plugin rather than a binary.
func (t Tool) IsPlugin() bool { return t.Kind == KindHelmPlugin }

// EnvVar is the override variable for a binary tool, e.g. OBOL_KUBECTL.
func EnvVar(name string) string {
	return "OBOL_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// AssetFor returns the asset for goos/goarch with {version} expanded.
func (t Tool) AssetFor(goos, goarch string) (Asset, error) {
	a, ok := t.Assets[goos+"/"+goarch]
	if !ok {
		return Asset{}, fmt.Errorf("%s %s has no release asset for %s/%s", t.Name, t.Version, goos, goarch)
	}

	a.URL = strings.ReplaceAll(a.URL, "{version}", t.Version)
	a.Member = strings.ReplaceAll(a.Member, "{version}", t.Version)

	return a, nil
}

// ParseManifest parses and validates manifest YAML.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse tool manifest: %w", err)
	}

	seen := map[string]bool{}

	for i := range m.Tools {
		t := &m.Tools[i]
		if t.Name == "" || t.Version == "" {
			return nil, fmt.Errorf("tool manifest entry %d: name and version are required", i)
		}

		if seen[t.Name] {
			return nil, fmt.Errorf("tool manifest: duplicate entry %q", t.Name)
		}

		seen[t.Name] = true

		if t.Kind == "" {
			t.Kind = KindBinary
		}

		if t.Compat == "" {
			t.Compat = CompatMinor
		}

		if t.Format == "" {
			t.Format = FormatRaw
		}

		switch t.Format {
		case FormatRaw, FormatTarGz, FormatZip:
		default:
			return nil, fmt.Errorf("tool %s: unknown format %q", t.Name, t.Format)
		}

		for plat, a := range t.Assets {
			if a.URL == "" || len(a.SHA256) != 64 {
				return nil, fmt.Errorf("tool %s %s: url and 64-char sha256 required", t.Name, plat)
			}

			if t.Format != FormatRaw && a.Member == "" {
				return nil, fmt.Errorf("tool %s %s: archive member required for format %s", t.Name, plat, t.Format)
			}
		}
	}

	return &m, nil
}

var (
	defaultOnce     sync.Once
	defaultManifest *Manifest
	defaultErr      error
)

// Default returns the embedded manifest. It panics only if the embedded file
// is malformed, which TestEmbeddedManifestValid guards against.
func Default() *Manifest {
	defaultOnce.Do(func() {
		defaultManifest, defaultErr = ParseManifest(manifestYAML)
	})

	if defaultErr != nil {
		panic(defaultErr)
	}

	return defaultManifest
}

// Get returns the named tool.
func (m *Manifest) Get(name string) (Tool, bool) {
	for _, t := range m.Tools {
		if t.Name == name {
			return t, true
		}
	}

	return Tool{}, false
}

// Names returns all tool names in manifest order.
func (m *Manifest) Names() []string {
	out := make([]string, 0, len(m.Tools))
	for _, t := range m.Tools {
		out = append(out, t.Name)
	}

	return out
}

// RequiredNames returns the tools `obol stack init/up` auto-installs.
// skip lets callers drop backend-specific tools (k3d on the k3s backend).
func (m *Manifest) RequiredNames(skip ...string) []string {
	var out []string

	for _, t := range m.Tools {
		if !t.Required {
			continue
		}

		skipped := false

		for _, s := range skip {
			if s == t.Name {
				skipped = true
			}
		}

		if !skipped {
			out = append(out, t.Name)
		}
	}

	return out
}

func hostPlatform() (string, string) { return runtime.GOOS, runtime.GOARCH }
