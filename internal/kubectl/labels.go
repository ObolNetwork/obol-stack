package kubectl

// ManagedByLabel marks objects obol applies from its host-side records
// (Agent CRs, CLI-created ServiceOffers, storefront profile, x402 pricing).
// Groundwork for label-scoped pruning in a declarative `obol stack apply`
// (plans/2026-10-lean-stack.md §1). Distinct value from the
// serviceoffer-controller's "serviceoffer-controller", which it uses as a
// selector for its own children — never reuse that value here.
const (
	ManagedByLabel = "obol.org/managed-by"
	ManagedByObol  = "obol"
)

// SetManagedBy stamps ManagedByLabel=ManagedByObol onto a decoded manifest
// (map from json/yaml.Unmarshal). A v1 List gets every item stamped.
// Malformed shapes are left untouched.
func SetManagedBy(obj map[string]any) {
	if obj == nil {
		return
	}
	if obj["kind"] == "List" {
		items, _ := obj["items"].([]any)
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				SetManagedBy(m)
			}
		}
		return
	}
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return
	}
	labels, ok := meta["labels"].(map[string]any)
	if !ok {
		labels = map[string]any{}
		// Preserve typed label maps produced by Go literals.
		if typed, ok := meta["labels"].(map[string]string); ok {
			for k, v := range typed {
				labels[k] = v
			}
		}
		meta["labels"] = labels
	}
	labels[ManagedByLabel] = ManagedByObol
}
