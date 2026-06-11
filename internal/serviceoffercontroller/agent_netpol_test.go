package serviceoffercontroller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// TestBuildAgentNetworkPolicy_IsolationInvariants pins the security shape
// of the agent-business namespace policy (plans/agent-business-architecture.md
// §3.5). The review boundaries that matter:
//   - cross-namespace agent traffic is NOT allowed by any ingress rule
//     other than traefik/x402 (port-restricted to the Hermes API) and
//     monitoring — i.e. agent A cannot reach agent B's remote-signer.
//   - egress allows the public internet but EXCLUDES the cluster CIDRs,
//     so the internet rule can never reopen cross-namespace traffic.
func TestBuildAgentNetworkPolicy_IsolationInvariants(t *testing.T) {
	agent := &monetizeapi.Agent{}
	agent.Name = "quant"
	agent.Namespace = "agent-quant"

	pol := buildAgentNetworkPolicy(agent)
	if pol.GetKind() != "NetworkPolicy" || pol.GetNamespace() != "agent-quant" {
		t.Fatalf("unexpected object: kind=%s ns=%s", pol.GetKind(), pol.GetNamespace())
	}

	raw, err := json.Marshal(pol.Object)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	// Applies to every pod in the namespace (hermes AND remote-signer)
	// and constrains both directions.
	spec, _ := pol.Object["spec"].(map[string]any)
	if sel, _ := spec["podSelector"].(map[string]any); len(sel) != 0 {
		t.Errorf("podSelector must be empty (all pods), got %v", sel)
	}
	for _, pt := range []string{"Ingress", "Egress"} {
		if !strings.Contains(body, `"`+pt+`"`) {
			t.Errorf("policyTypes missing %s", pt)
		}
	}

	// Ingress: the only cross-namespace sources are traefik/x402
	// (hermes port only) and monitoring. The signer port must never
	// appear in an ingress rule.
	for _, ns := range []string{"traefik", "x402", "monitoring"} {
		if !strings.Contains(body, `"kubernetes.io/metadata.name":"`+ns+`"`) {
			t.Errorf("expected namespace selector for %q", ns)
		}
	}
	if strings.Contains(body, `"port":9000`) {
		t.Error("remote-signer port 9000 must not appear in the policy — signer access is same-namespace only (via the allow-all same-ns rule)")
	}
	if !strings.Contains(body, `"port":8642`) {
		t.Error("paid-traffic ingress must be restricted to the Hermes port 8642")
	}

	// Egress: cluster CIDRs excluded from the internet rule.
	for _, cidr := range []string{clusterPodCIDR, clusterServiceCIDR} {
		if !strings.Contains(body, cidr) {
			t.Errorf("internet egress rule must except cluster CIDR %s", cidr)
		}
	}
	if !strings.Contains(body, `"cidr":"0.0.0.0/0"`) {
		t.Error("egress must allow the public internet (0.0.0.0/0 with cluster excepts)")
	}
	// In-cluster egress allowlist: inference, chain reads, buying via
	// Traefik, DNS.
	for _, ns := range []string{"llm", "erpc", "kube-system"} {
		if !strings.Contains(body, `"kubernetes.io/metadata.name":"`+ns+`"`) {
			t.Errorf("expected egress namespace selector for %q", ns)
		}
	}
	if !strings.Contains(body, `"port":4000`) {
		t.Error("egress must allow LiteLLM on llm:4000")
	}

	// Link-local (incl. the cloud IMDS endpoint 169.254.169.254) must be
	// excepted from the internet rule so semi-untrusted skill code cannot
	// SSRF instance-metadata credentials on a cloud node.
	if !strings.Contains(body, linkLocalCIDR) {
		t.Errorf("internet egress rule must except link-local %s (IMDS hardening)", linkLocalCIDR)
	}

	// DNS must be reachable or the agent is dead: the kube-dns podSelector
	// and port 53 must both be present, not just the kube-system namespace.
	if !strings.Contains(body, `"k8s-app":"kube-dns"`) {
		t.Error("egress DNS rule must target the kube-dns podSelector, not the whole kube-system namespace")
	}
	if !strings.Contains(body, `"port":53`) {
		t.Error("egress must allow DNS on port 53")
	}
}

// TestAgentManifests_IncludesNetworkPolicy guards that the isolation policy
// is actually rendered alongside the agent's primitives — buildAgentNetworkPolicy
// being correct is moot if it is never added to the applied manifest set.
func TestAgentManifests_IncludesNetworkPolicy(t *testing.T) {
	agent := &monetizeapi.Agent{}
	agent.Name = "quant"
	agent.Namespace = "agent-quant"
	agent.Spec.Model = "qwen3.5:9b"

	manifests, err := agentManifests(agent, "litellm-key", "api-key")
	if err != nil {
		t.Fatalf("agentManifests: %v", err)
	}
	found := false
	for _, m := range manifests {
		if m.GetKind() == "NetworkPolicy" {
			found = true
			if m.GetName() != "agent-isolation" || m.GetNamespace() != "agent-quant" {
				t.Errorf("unexpected NetworkPolicy identity: %s/%s", m.GetNamespace(), m.GetName())
			}
		}
	}
	if !found {
		t.Error("agentManifests must include the agent-isolation NetworkPolicy")
	}
}
