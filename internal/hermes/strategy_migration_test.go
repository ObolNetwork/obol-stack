package hermes

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestStrategyMigrationPatchArgs guards the imperative pre-helm fix for the
// RollingUpdate -> Recreate migration. A hermes deployment first created with
// the default strategy keeps a k8s-populated strategy.rollingUpdate that helm
// won't clear when the manifest only flips type to Recreate, so the API rejects
// the update and obol model setup/prefer/sync silently fail to reach the agent.
// The patch must be a strategic merge that pins type=Recreate AND carries an
// explicit rollingUpdate:null — the null is what removes the offending field.
func TestStrategyMigrationPatchArgs(t *testing.T) {
	args := strategyMigrationPatchArgs("hermes-obol-agent")

	// Sanity: targets the hermes deployment in the requested namespace via a
	// merge patch.
	want := map[string]bool{
		"patch":             false,
		"deployment/hermes": false,
		"hermes-obol-agent": false,
		"--type=merge":      false,
	}
	var patchJSON string
	for i, a := range args {
		if _, ok := want[a]; ok {
			want[a] = true
		}
		if a == "-p" && i+1 < len(args) {
			patchJSON = args[i+1]
		}
	}
	for arg, seen := range want {
		if !seen {
			t.Errorf("strategyMigrationPatchArgs missing expected arg %q in %v", arg, args)
		}
	}
	if patchJSON == "" {
		t.Fatalf("strategyMigrationPatchArgs produced no -p patch body: %v", args)
	}

	var patch map[string]any
	if err := json.Unmarshal([]byte(patchJSON), &patch); err != nil {
		t.Fatalf("patch body is not valid JSON: %v\n%s", err, patchJSON)
	}
	strategy, ok := patch["spec"].(map[string]any)["strategy"].(map[string]any)
	if !ok {
		t.Fatalf("patch missing spec.strategy: %s", patchJSON)
	}
	if strategy["type"] != "Recreate" {
		t.Errorf("patch strategy.type = %v, want Recreate", strategy["type"])
	}
	// The rollingUpdate KEY must be present with a null value. A strategic
	// merge patch removes a field only when it is explicitly null; omitting the
	// key leaves the stale live rollingUpdate in place and the migration fails.
	ru, present := strategy["rollingUpdate"]
	if !present {
		t.Errorf("patch strategy is missing the rollingUpdate key; without an explicit null the stale field is not cleared: %s", patchJSON)
	}
	if ru != nil {
		t.Errorf("patch strategy.rollingUpdate = %v, want explicit null", ru)
	}
}

// TestStrategyMigrationPatchArgs_ThreadsNamespace ensures the namespace flag is
// passed through, so non-default hermes instances are migrated in their own
// namespace rather than a hardcoded one.
func TestStrategyMigrationPatchArgs_ThreadsNamespace(t *testing.T) {
	args := strategyMigrationPatchArgs("hermes-custom")
	var nsValue string
	for i, a := range args {
		if a == "-n" && i+1 < len(args) {
			nsValue = args[i+1]
		}
	}
	if nsValue != "hermes-custom" {
		t.Errorf("namespace arg = %q, want hermes-custom; args=%v", nsValue, args)
	}
}

// TestGenerateValues_StrategyIsRecreateWithoutRollingUpdate verifies the
// rendered deployment pins Recreate and never emits a rollingUpdate block, so
// a fresh install starts clean and the imperative migration is only needed for
// pre-existing RollingUpdate deployments.
func TestGenerateValues_StrategyIsRecreateWithoutRollingUpdate(t *testing.T) {
	values := generateValues(
		"hermes-obol-agent",
		"hermes-obol-agent.obol.stack",
		"obol-agent.obol.stack",
		"https://agent.example.com",
		"secret-token",
		"gpt-5.2",
		[]byte("model:\n  default: gpt-5.2\n"),
	)

	// The values file is a bedag/raw chart: resources live under .resources.
	var doc struct {
		Resources []map[string]any `yaml:"resources"`
	}
	if err := yaml.Unmarshal([]byte(values), &doc); err != nil {
		t.Fatalf("generateValues produced invalid YAML: %v", err)
	}

	var found bool
	for _, res := range doc.Resources {
		if res["kind"] != "Deployment" {
			continue
		}
		found = true
		spec, _ := res["spec"].(map[string]any)
		strategy, ok := spec["strategy"].(map[string]any)
		if !ok {
			t.Fatalf("Deployment has no spec.strategy: %#v", spec)
		}
		if strategy["type"] != "Recreate" {
			t.Errorf("Deployment strategy.type = %v, want Recreate", strategy["type"])
		}
		if _, has := strategy["rollingUpdate"]; has {
			t.Errorf("rendered Deployment emits a rollingUpdate block (%v); it must be absent for a Recreate strategy", strategy["rollingUpdate"])
		}
	}
	if !found {
		t.Fatal("generateValues produced no Deployment resource")
	}
}
