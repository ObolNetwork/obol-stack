package stackbackup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// clusterDump describes one `kubectl get -o json` harvest target. These are
// the etcd-resident resources with no host-side source of truth (see the
// state inventory in plans/stack-export-import.md). PurchaseRequests and
// buyer-auth ConfigMaps are deliberately absent: pre-signed payment auths
// expire via validBefore, so restoring them restores garbage — re-buy after
// import instead.
type clusterDump struct {
	file string
	args []string
}

var clusterDumps = []clusterDump{
	{"agents.json", []string{"get", "agents.obol.org", "-A", "-o", "json"}},
	{"serviceoffers.json", []string{"get", "serviceoffers.obol.org", "-A", "-o", "json"}},
	{"registrationrequests.json", []string{"get", "registrationrequests.obol.org", "-A", "-o", "json"}},
	{"litellm-config.json", []string{"get", "configmap", "litellm-config", "-n", "llm", "-o", "json"}},
	{"litellm-secrets.json", []string{"get", "secret", "litellm-secrets", "-n", "llm", "-o", "json"}},
	{"erpc-config.json", []string{"get", "configmap", "erpc-config", "-n", "erpc", "-o", "json"}},
	{"x402-pricing.json", []string{"get", "configmap", "x402-pricing", "-n", "x402", "-o", "json"}},
	{"storefront-profile.json", []string{"get", "configmap", "obol-storefront-profile", "-n", "x402", "-o", "json"}},
}

// applyOrder is the import-time apply sequence: Agent CRs first (agent-backed
// ServiceOffers resolve agent refs), then config, then offers/registrations
// for the controller to reconcile.
var applyOrder = []string{
	"agents.json",
	"agent-secrets.json",
	"litellm-config.json",
	"litellm-secrets.json",
	"erpc-config.json",
	"x402-pricing.json",
	"storefront-profile.json",
	"serviceoffers.json",
	"registrationrequests.json",
}

// harvestCluster dumps the drift-prone cluster resources into destDir as
// stripped JSON. Missing resources (CRD not installed, nothing sold yet) are
// recorded as notes, not errors.
func harvestCluster(cfg *config.Config, destDir string) (notes []string, err error) {
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, err
	}
	bin, kubeconfig := kubectl.Paths(cfg)
	for _, d := range clusterDumps {
		out, getErr := kubectl.Output(bin, kubeconfig, d.args...)
		if getErr != nil {
			notes = append(notes, fmt.Sprintf("%s: not captured (%v)", d.file, getErr))
			continue
		}
		stripped, stripErr := StripK8sJSON([]byte(out))
		if stripErr != nil {
			notes = append(notes, fmt.Sprintf("%s: not captured (%v)", d.file, stripErr))
			continue
		}
		if stripped == nil {
			notes = append(notes, fmt.Sprintf("%s: empty", d.file))
			continue
		}
		if err := os.WriteFile(filepath.Join(destDir, d.file), stripped, 0o600); err != nil {
			return notes, err
		}
		notes = append(notes, d.file)
	}

	// CRD-declared agents may keep their remote-signer keystore only in a
	// namespace Secret (no host file), so harvest Opaque secrets from every
	// agent-* namespace named in the Agent CR dump.
	if n := harvestAgentSecrets(cfg, destDir); n != "" {
		notes = append(notes, n)
	}
	return notes, nil
}

func harvestAgentSecrets(cfg *config.Config, destDir string) string {
	agentsRaw, err := os.ReadFile(filepath.Join(destDir, "agents.json"))
	if err != nil {
		return ""
	}
	namespaces := namespacesFromList(agentsRaw)
	if len(namespaces) == 0 {
		return ""
	}
	bin, kubeconfig := kubectl.Paths(cfg)
	var items []json.RawMessage
	for _, ns := range namespaces {
		out, err := kubectl.Output(bin, kubeconfig, "get", "secret", "-n", ns, "--field-selector", "type=Opaque", "-o", "json")
		if err != nil {
			continue
		}
		stripped, err := StripK8sJSON([]byte(out))
		if err != nil || stripped == nil {
			continue
		}
		var list struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(stripped, &list); err != nil {
			continue
		}
		items = append(items, list.Items...)
	}
	if len(items) == 0 {
		return ""
	}
	out, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"items":      items,
	})
	if err != nil {
		return ""
	}
	if err := os.WriteFile(filepath.Join(destDir, "agent-secrets.json"), out, 0o600); err != nil {
		return ""
	}
	return fmt.Sprintf("agent-secrets.json (%d secrets from %d agent namespaces)", len(items), len(namespaces))
}

// applyCluster re-applies harvested dumps from srcDir in dependency order,
// creating namespaces for namespaced items first. Per-file failures are
// reported and do not stop later files.
func applyCluster(cfg *config.Config, srcDir string, u *ui.UI) {
	bin, kubeconfig := kubectl.Paths(cfg)
	for _, file := range applyOrder {
		data, err := os.ReadFile(filepath.Join(srcDir, file))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			u.Warnf("Could not read %s: %v", file, err)
			continue
		}
		for _, ns := range namespacesFromList(data) {
			ensureNamespace(bin, kubeconfig, ns, u)
		}
		if err := kubectl.Apply(bin, kubeconfig, data); err != nil {
			u.Warnf("Could not apply %s: %v", file, err)
			continue
		}
		u.Detail("Applied", file)
	}
}

func ensureNamespace(bin, kubeconfig, ns string, u *ui.UI) {
	err := kubectl.PipeCommands(bin, kubeconfig,
		[]string{"create", "namespace", ns, "--dry-run=client", "-o", "yaml"},
		[]string{"apply", "-f", "-"})
	if err != nil {
		u.Warnf("Could not ensure namespace %s: %v", ns, err)
	}
}

// namespacesFromList extracts the distinct metadata.namespace values from a
// (possibly List-kind) Kubernetes JSON document.
func namespacesFromList(data []byte) []string {
	var doc struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(ns string) {
		if ns != "" && !seen[ns] {
			seen[ns] = true
			out = append(out, ns)
		}
	}
	add(doc.Metadata.Namespace)
	for _, it := range doc.Items {
		add(it.Metadata.Namespace)
	}
	return out
}

// StripK8sJSON removes server-managed fields (status, managedFields,
// resourceVersion, uid, ownerReferences, last-applied annotation, ...) from a
// kubectl JSON dump so it can be re-applied to a fresh cluster. Returns nil
// for an empty List.
func StripK8sJSON(data []byte) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse kubectl output: %w", err)
	}
	if items, ok := doc["items"].([]any); ok {
		if len(items) == 0 {
			return nil, nil
		}
		for _, it := range items {
			if obj, ok := it.(map[string]any); ok {
				StripServerManagedMetadata(obj)
			}
		}
	} else {
		StripServerManagedMetadata(doc)
	}
	return json.MarshalIndent(doc, "", "  ")
}

// StripServerManagedMetadata strips server-managed fields from one decoded
// Kubernetes object (map from json.Unmarshal or yaml.Unmarshal) so it can be
// re-applied. Used by StripK8sJSON (export/import dumps) and by
// internal/agentcrd.ResumeAll (persisted Agent manifests on stack up).
func StripServerManagedMetadata(obj map[string]any) {
	delete(obj, "status")
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return
	}
	for _, k := range []string{
		"managedFields", "resourceVersion", "uid", "creationTimestamp",
		"generation", "selfLink", "ownerReferences", "finalizers",
	} {
		delete(meta, k)
	}
	if ann, ok := meta["annotations"].(map[string]any); ok {
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		if len(ann) == 0 {
			delete(meta, "annotations")
		}
	}
}
