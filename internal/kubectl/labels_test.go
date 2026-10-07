package kubectl

import "testing"

func TestSetManagedBy(t *testing.T) {
	obj := map[string]any{"kind": "Agent", "metadata": map[string]any{"name": "a"}}
	SetManagedBy(obj)
	if got := obj["metadata"].(map[string]any)["labels"].(map[string]any)[ManagedByLabel]; got != ManagedByObol {
		t.Fatalf("label = %v", got)
	}

	typed := map[string]any{"metadata": map[string]any{"labels": map[string]string{"app": "x"}}}
	SetManagedBy(typed)
	labels := typed["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels["app"] != "x" || labels[ManagedByLabel] != ManagedByObol {
		t.Fatalf("typed labels not preserved: %v", labels)
	}

	list := map[string]any{"kind": "List", "items": []any{
		map[string]any{"kind": "Service", "metadata": map[string]any{"name": "s"}},
		map[string]any{"kind": "ServiceOffer", "metadata": map[string]any{"name": "o"}},
	}}
	SetManagedBy(list)
	for _, it := range list["items"].([]any) {
		l := it.(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)
		if l[ManagedByLabel] != ManagedByObol {
			t.Fatalf("list item not stamped: %v", it)
		}
	}

	SetManagedBy(nil)                               // no panic
	SetManagedBy(map[string]any{"metadata": "bad"}) // malformed left alone
}
