package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"gopkg.in/yaml.v3"
)

// ApplyOverrides merges values files and --set overrides into a
// deployment's values.yaml. Overrides are persisted to disk rather than
// passed as ephemeral helmfile flags so the deployment directory stays the
// single declarative record — `obol app sync` and stack-up resume replay
// exactly what was last applied.
//
// Files are merged in order, then set expressions on top (later wins).
func ApplyOverrides(cfg *config.Config, deploymentIdentifier string, valuesFiles, sets []string) error {
	if len(valuesFiles) == 0 && len(sets) == 0 {
		return nil
	}

	appName, id, err := parseDeploymentIdentifier(deploymentIdentifier)
	if err != nil {
		return err
	}

	valuesPath := filepath.Join(cfg.ConfigDir, "applications", appName, id, "values.yaml")

	return applyOverridesToFile(valuesPath, valuesFiles, sets)
}

// applyOverridesToFile does the actual load → merge → write on a
// values.yaml path. Split out so Install can call it on a freshly written
// file without going through identifier resolution.
func applyOverridesToFile(valuesPath string, valuesFiles, sets []string) error {
	base, err := loadValuesMap(valuesPath)
	if err != nil {
		return err
	}

	for _, file := range valuesFiles {
		overlay, err := loadValuesMap(file)
		if err != nil {
			return err
		}

		base = mergeMaps(base, overlay)
	}

	for _, expr := range sets {
		if err := applySetExpression(base, expr); err != nil {
			return err
		}
	}

	out, err := yaml.Marshal(base)
	if err != nil {
		return fmt.Errorf("failed to marshal merged values: %w", err)
	}

	return os.WriteFile(valuesPath, out, 0o600)
}

// loadValuesMap reads a YAML file into a string-keyed map. A file that is
// empty or contains only comments (e.g. a chart with no default values)
// yields an empty map.
func loadValuesMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read values file: %w", err)
	}

	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	if values == nil {
		values = map[string]any{}
	}

	return values, nil
}

// mergeMaps deep-merges src into dst: nested maps merge recursively,
// everything else (scalars, lists) is replaced by src. Returns dst.
func mergeMaps(dst, src map[string]any) map[string]any {
	for key, srcVal := range src {
		if dstMap, ok := dst[key].(map[string]any); ok {
			if srcMap, ok := srcVal.(map[string]any); ok {
				dst[key] = mergeMaps(dstMap, srcMap)
				continue
			}
		}

		dst[key] = srcVal
	}

	return dst
}

// applySetExpression applies a single "path.to.key=value" override onto
// values, creating intermediate maps as needed. The value is parsed as a
// YAML scalar so `true`, `3`, and quoted strings get their natural types.
// Existing non-map intermediates are overwritten with maps, matching helm
// --set semantics.
func applySetExpression(values map[string]any, expr string) error {
	key, rawValue, ok := strings.Cut(expr, "=")
	if !ok || key == "" {
		return fmt.Errorf("invalid --set expression %q: expected key.path=value", expr)
	}

	var value any
	if err := yaml.Unmarshal([]byte(rawValue), &value); err != nil {
		return fmt.Errorf("invalid --set value in %q: %w", expr, err)
	}

	segments := strings.Split(key, ".")
	current := values

	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[segment] = next
		}

		current = next
	}

	current[segments[len(segments)-1]] = value

	return nil
}
