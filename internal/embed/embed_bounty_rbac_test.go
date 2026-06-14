package embed

import (
	"strings"
	"testing"
)

// The bounty RBAC posture is a reviewed security decision (see
// plans/bounty-ane-marketplace-design.md, review fix #2): the controller gets
// cluster-wide watch/status on servicebounties, the AGENT grant is a
// NAMESPACED Role in the hermes mother namespace — never the cluster-wide
// openclaw-monetize-write ClusterRole. These tests pin that decision.

func TestBountyRBAC_ControllerClusterRoleIncludesServiceBounties(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/x402.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)
	var controllerRole map[string]any
	for _, d := range docs {
		if d["kind"] == "ClusterRole" && nested(d, "metadata", "name") == "serviceoffer-controller" {
			controllerRole = d
			break
		}
	}
	if controllerRole == nil {
		t.Fatal("serviceoffer-controller ClusterRole not found in x402.yaml")
	}

	var hasBounties, hasBountyStatus bool
	var hasEnrollments, hasEnrollmentStatus bool
	var enrollmentVerbs []any
	rules, _ := controllerRole["rules"].([]any)
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		resources, _ := rule["resources"].([]any)
		for _, res := range resources {
			switch res {
			case "servicebounties":
				hasBounties = true
			case "servicebounties/status":
				hasBountyStatus = true
			case "evaluatorenrollments":
				hasEnrollments = true
				enrollmentVerbs, _ = rule["verbs"].([]any)
			case "evaluatorenrollments/status":
				hasEnrollmentStatus = true
			}
		}
	}
	if !hasBounties || !hasBountyStatus {
		t.Errorf("serviceoffer-controller ClusterRole missing servicebounties (%v) or servicebounties/status (%v)", hasBounties, hasBountyStatus)
	}
	if !hasEnrollments || !hasEnrollmentStatus {
		t.Errorf("serviceoffer-controller ClusterRole missing evaluatorenrollments (%v) or evaluatorenrollments/status (%v)", hasEnrollments, hasEnrollmentStatus)
	}
	// The controller READS the pool and writes ladder STATE only — it never
	// creates or deletes enrollments (evaluators own their enrollment).
	for _, verb := range enrollmentVerbs {
		if verb == "create" || verb == "delete" {
			t.Errorf("controller must not %v evaluatorenrollments — the pool is evaluator-owned", verb)
		}
	}
}

func TestBountyRBAC_AgentGrantIsNamespacedNotClusterWide(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/obol-agent-monetize-rbac.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)

	// 1. The cluster-wide write ClusterRole must NOT mention servicebounties.
	for _, d := range docs {
		if d["kind"] != "ClusterRole" {
			continue
		}
		name, _ := nested(d, "metadata", "name").(string)
		rules, _ := d["rules"].([]any)
		for _, r := range rules {
			rule, _ := r.(map[string]any)
			resources, _ := rule["resources"].([]any)
			for _, res := range resources {
				if s, _ := res.(string); strings.Contains(s, "servicebounties") {
					t.Errorf("ClusterRole %q grants %q — bounty write must stay a namespaced Role", name, s)
				}
			}
		}
	}

	// 2. The namespaced Role exists, in the hermes mother namespace.
	var role map[string]any
	for _, d := range docs {
		if d["kind"] == "Role" && nested(d, "metadata", "name") == "hermes-bounty-write" {
			role = d
			break
		}
	}
	if role == nil {
		t.Fatal("namespaced Role hermes-bounty-write not found")
	}
	if ns := nested(role, "metadata", "namespace"); ns != "hermes-obol-agent" {
		t.Errorf("hermes-bounty-write namespace = %v, want hermes-obol-agent", ns)
	}

	var binding map[string]any
	for _, d := range docs {
		if d["kind"] == "RoleBinding" && nested(d, "metadata", "name") == "hermes-bounty-write-binding" {
			binding = d
			break
		}
	}
	if binding == nil {
		t.Fatal("RoleBinding hermes-bounty-write-binding not found")
	}
}
