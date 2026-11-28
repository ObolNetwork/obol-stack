package app

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/fsutil"
	"gopkg.in/yaml.v3"
)

// metaFileName is obol's record of an app deployment, next to helmfile.yaml
// and values.yaml. Apps installed before v0.19 have none; their release
// name and chart come from helmfile.yaml (which operators may have
// hand-edited, so it stays authoritative for the release name).
const metaFileName = "obol-app.yaml"

// Values modes recorded in AppMeta.ValuesMode.
const (
	// ValuesModeOverrides: values.yaml holds only the operator's overrides;
	// chart defaults come from the chart at sync time.
	ValuesModeOverrides = "overrides"
	// ValuesModeFullDefaults: values.yaml is a copy of the chart's full
	// defaults (installs before v0.19). A chart bump keeps the old defaults,
	// image tags included, pinned as if they were user values.
	ValuesModeFullDefaults = "full-defaults"
)

// AppMeta is the persisted metadata of one app deployment.
type AppMeta struct {
	// Release is the helm release name. Informational: helmfile.yaml is
	// what deploys, and it wins if the two disagree.
	Release   string `yaml:"release"`
	Namespace string `yaml:"namespace"`
	ChartRef  string `yaml:"chartRef,omitempty"`
	ChartName string `yaml:"chartName,omitempty"`
	// ValuesMode is ValuesModeOverrides or ValuesModeFullDefaults.
	ValuesMode string `yaml:"valuesMode"`
	// ValuesChartVersion is the chart version values.yaml was generated
	// from (for full-defaults values, the version whose defaults it holds).
	ValuesChartVersion string `yaml:"valuesChartVersion,omitempty"`
	// SyncedRevision is the helm revision obol app produced on its last
	// successful sync; a higher live revision means someone changed the
	// release outside obol app.
	SyncedRevision int `yaml:"syncedRevision,omitempty"`
	// SyncedChartVersion is the chart version of that revision.
	SyncedChartVersion string `yaml:"syncedChartVersion,omitempty"`

	// legacy is true when no metadata file existed (pre-v0.19 install).
	// Unexported, so yaml never serializes it.
	legacy bool
}

// Legacy reports whether this deployment predates obol-app.yaml.
func (m *AppMeta) Legacy() bool { return m.legacy }

func deploymentDirFor(cfg *config.Config, appName, id string) string {
	return filepath.Join(cfg.ConfigDir, "applications", appName, id)
}

// loadAppMeta reads a deployment's metadata. Without obol-app.yaml it
// derives it from helmfile.yaml (release name, namespace, chart) and marks
// it legacy with full-default values. The release name and namespace in
// helmfile.yaml always win over the metadata file, since that is what
// `helmfile sync` deploys.
func loadAppMeta(dir, appName, id string) (*AppMeta, error) {
	meta := &AppMeta{}

	data, err := os.ReadFile(filepath.Join(dir, metaFileName))

	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, meta); err != nil {
			return nil, fmt.Errorf("parse %s: %w", metaFileName, err)
		}
	case errors.Is(err, os.ErrNotExist):
		meta.legacy = true
		meta.ValuesMode = ValuesModeFullDefaults
	default:
		return nil, fmt.Errorf("read %s: %w", metaFileName, err)
	}

	if info, err := ParseHelmfile(dir); err == nil {
		if info.Release != "" {
			meta.Release = info.Release
		}

		if info.Namespace != "" {
			meta.Namespace = info.Namespace
		}

		if meta.ChartRef == "" {
			meta.ChartRef = info.ChartRef
		}

		if meta.ChartName == "" {
			meta.ChartName = chartNameFromRef(info.Chart)
		}

		if meta.legacy && meta.ValuesChartVersion == "" {
			meta.ValuesChartVersion = info.Version
		}
	}

	// Pre-v0.19 defaults: release <app>, namespace <app>-<id>.
	if meta.Release == "" {
		meta.Release = appName
	}

	if meta.Namespace == "" {
		meta.Namespace = appName + "-" + id
	}

	return meta, nil
}

// saveAppMeta writes obol-app.yaml (0600, atomic).
func saveAppMeta(dir string, meta *AppMeta) error {
	out, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}

	header := "# Managed by obol app. Records the helm release and the last revision obol synced.\n"

	return fsutil.WriteSecret(filepath.Join(dir, metaFileName), append([]byte(header), out...))
}

// chartNameFromRef extracts the bare chart name from a helmfile chart field
// or a chart reference: "charon/dv-pod" → "dv-pod",
// "https://…/redis-19.0.0.tgz" → "redis", "oci://…/redis:1.0" → "redis".
func chartNameFromRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}

	if i := strings.LastIndex(ref, "@"); i > 0 && !strings.Contains(ref[i:], "/") {
		ref = ref[:i]
	}

	base := path.Base(ref)

	if trimmed, ok := strings.CutSuffix(base, ".tgz"); ok {
		base = trimmed
		if m := chartVersionSuffixRE.FindStringSubmatch(base); len(m) > 2 {
			base = m[1]
		}

		return base
	}

	if strings.HasPrefix(ref, "oci://") {
		if i := strings.LastIndex(base, ":"); i > 0 {
			base = base[:i]
		}
	}

	return base
}

var chartVersionSuffixRE = regexp.MustCompile(`^(.+)-(\d+\.\d+\.\d+.*)$`)

// namespaceRE is a DNS-1123 label (Kubernetes namespace names).
var namespaceRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// reservedNamespaces are namespaces the stack itself owns.
var reservedNamespaces = map[string]bool{
	"default": true, "kube-system": true, "kube-public": true, "kube-node-lease": true,
	"local-path-storage": true, "traefik": true, "x402": true, "llm": true, "erpc": true,
	"monitoring": true, "obol-frontend": true, "reloader": true, "obol-agent": true,
}

// reservedNamespacePrefixes cover per-deployment stack namespaces (agents,
// network nodes).
var reservedNamespacePrefixes = []string{"kube-", "hermes-", "openclaw-", "ethereum-", "aztec-"}

// ValidateNamespace checks an app namespace: a DNS-1123 label that the
// stack doesn't own.
func ValidateNamespace(ns string) error {
	if len(ns) > 63 || !namespaceRE.MatchString(ns) {
		return fmt.Errorf("invalid namespace %q: use lowercase letters, digits and '-', start and end alphanumeric, at most 63 characters", ns)
	}

	if reservedNamespaces[ns] {
		return fmt.Errorf("namespace %q belongs to the stack; apps can't be installed there", ns)
	}

	for _, p := range reservedNamespacePrefixes {
		if strings.HasPrefix(ns, p) {
			return fmt.Errorf("namespace %q is reserved for the stack (%s*); apps can't be installed there", ns, p)
		}
	}

	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

// releaseNameRE is helm's release-name rule (DNS-1123 label, ≤53 chars).
var releaseNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// ValidateReleaseName checks a helm release name.
func ValidateReleaseName(name string) error {
	if len(name) > 53 || !releaseNameRE.MatchString(name) {
		return fmt.Errorf("invalid release name %q: use lowercase letters, digits and '-', start and end alphanumeric, at most 53 characters", name)
	}

	return nil
}
