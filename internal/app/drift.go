package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// releaseState is the latest helm revision of a release.
type releaseState struct {
	Revision     int
	Status       string
	ChartVersion string
}

var chartVersionTailRE = regexp.MustCompile(`-(v?\d+\.\d+\.\d+[^\s]*)$`)

// liveRelease reads the latest revision of a helm release (`helm history
// --max 1`, which carries no values). It returns nil, nil when the release
// does not exist.
func liveRelease(cfg *config.Config, namespace, release string) (*releaseState, error) {
	out, err := helmCluster(cfg, "history", release, "-n", namespace, "--max", "1", "-o", "json")
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, nil //nolint:nilnil // nil state = no such release
		}

		return nil, err
	}

	var history []struct {
		Revision int    `json:"revision"`
		Status   string `json:"status"`
		Chart    string `json:"chart"`
	}
	if err := json.Unmarshal(out, &history); err != nil {
		return nil, fmt.Errorf("parse helm history: %w", err)
	}

	if len(history) == 0 {
		return nil, nil //nolint:nilnil // nil state = no such release
	}

	last := history[len(history)-1]
	state := &releaseState{Revision: last.Revision, Status: last.Status}

	if m := chartVersionTailRE.FindStringSubmatch(last.Chart); len(m) == 2 {
		state.ChartVersion = m[1]
	}

	return state, nil
}

// liveValues reads the user-supplied values of a release (`helm get
// values`). They stay in memory; only key paths are ever printed.
func liveValues(cfg *config.Config, namespace, release string) (map[string]any, error) {
	out, err := helmCluster(cfg, "get", "values", release, "-n", namespace, "-o", "json")
	if err != nil {
		return nil, err
	}

	var values map[string]any

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return map[string]any{}, nil
	}

	if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
		return nil, fmt.Errorf("parse helm values: %w", err)
	}

	if values == nil {
		values = map[string]any{}
	}

	return values, nil
}

// maxDriftPaths caps how many differing key paths a drift message names.
const maxDriftPaths = 10

// valuesDiff returns the sorted key paths whose values differ between the
// deployment's values file and the live release's values. Never values.
func valuesDiff(file, live map[string]any) []string {
	a, b := toJSONShape(file), toJSONShape(live)

	var out []string

	diffValueTrees(a, b, "", &out)
	sort.Strings(out)

	return out
}

// toJSONShape normalizes a yaml.v3-decoded tree to what encoding/json
// produces (float64 numbers, map[string]any), so it compares with helm's
// JSON output.
func toJSONShape(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}

	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}

	return out
}

func diffValueTrees(a, b any, prefix string, out *[]string) {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)

	// A map on one side and nothing on the other: name the leaves.
	if aok && b == nil {
		bm, bok = map[string]any{}, true
	}

	if bok && a == nil {
		am, aok = map[string]any{}, true
	}

	if aok && bok {
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}

		for k := range bm {
			keys[k] = true
		}

		for k := range keys {
			diffValueTrees(am[k], bm[k], joinPath(prefix, k), out)
		}

		return
	}

	if leafEqual(a, b) {
		return
	}

	*out = append(*out, prefix)
}

func leafEqual(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}

	if isEmptyValue(a) && isEmptyValue(b) {
		return true
	}

	// A bare hex string in values.yaml is a float on the helm side (the
	// quoting bug warned about separately); don't call that drift.
	if s, ok := a.(string); ok && strings.HasPrefix(strings.ToLower(s), "0x") {
		if _, isFloat := b.(float64); isFloat {
			return true
		}
	}

	if s, ok := b.(string); ok && strings.HasPrefix(strings.ToLower(s), "0x") {
		if _, isFloat := a.(float64); isFloat {
			return true
		}
	}

	return false
}

// formatPaths renders a capped, comma-separated list of key paths.
func formatPaths(paths []string) string {
	if len(paths) <= maxDriftPaths {
		return strings.Join(paths, ", ")
	}

	return strings.Join(paths[:maxDriftPaths], ", ") + fmt.Sprintf(" (+%d more)", len(paths)-maxDriftPaths)
}

// isEmptyValue treats nil, {} and [] alike: helm may drop empty or null
// keys from the values it stores.
func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case map[string]any:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}

	return false
}
