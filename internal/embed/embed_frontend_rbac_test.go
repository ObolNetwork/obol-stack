package embed

import "testing"

func TestObolFrontendRBAC_CanReadDefaultHermesTokenSecret(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/obol-frontend.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}
	docs := multiDoc(data)

	role := findDocByName(docs, "Role", "obol-frontend-hermes-token-reader")
	if role == nil {
		t.Fatal("no Role 'obol-frontend-hermes-token-reader' found")
	}
	if ns := nested(role, "metadata", "namespace"); ns != "hermes-obol-agent" {
		t.Fatalf("token reader Role namespace = %v, want hermes-obol-agent", ns)
	}

	rules, ok := role["rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("token reader Role rules = %#v, want exactly one rule", role["rules"])
	}
	rule, ok := rules[0].(map[string]any)
	if !ok {
		t.Fatalf("token reader Role rule has type %T", rules[0])
	}
	if !stringSet(rule["apiGroups"])[""] {
		t.Fatal("token reader Role must target the core API group")
	}
	if !stringSet(rule["resources"])["secrets"] {
		t.Fatal("token reader Role must target core/secrets")
	}
	if !stringSet(rule["resourceNames"])["hermes-api-server"] {
		t.Fatal("token reader Role must be scoped to secret/hermes-api-server")
	}
	verbs := stringSet(rule["verbs"])
	if !verbs["get"] {
		t.Fatal("token reader Role missing get verb")
	}
	for _, forbidden := range []string{"list", "watch", "create", "update", "patch", "delete"} {
		if verbs[forbidden] {
			t.Fatalf("token reader Role grants forbidden verb %q", forbidden)
		}
	}

	binding := findDocByName(docs, "RoleBinding", "obol-frontend-hermes-token-reader")
	if binding == nil {
		t.Fatal("no RoleBinding 'obol-frontend-hermes-token-reader' found")
	}
	if ns := nested(binding, "metadata", "namespace"); ns != "hermes-obol-agent" {
		t.Fatalf("token reader RoleBinding namespace = %v, want hermes-obol-agent", ns)
	}
	if ref := nested(binding, "roleRef", "kind"); ref != "Role" {
		t.Fatalf("token reader RoleBinding roleRef.kind = %v, want Role", ref)
	}
	if ref := nested(binding, "roleRef", "name"); ref != "obol-frontend-hermes-token-reader" {
		t.Fatalf("token reader RoleBinding roleRef.name = %v, want obol-frontend-hermes-token-reader", ref)
	}
	if !bindingHasSubject(binding, "obol-frontend", "obol-frontend") {
		t.Fatal("token reader RoleBinding missing obol-frontend/obol-frontend subject")
	}
}

func TestObolFrontendDiscoveryRBAC_DoesNotGrantBroadSecretAccess(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/obol-frontend.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}
	docs := multiDoc(data)

	role := findDocByName(docs, "ClusterRole", "obol-frontend-openclaw-discovery")
	if role == nil {
		t.Fatal("no ClusterRole 'obol-frontend-openclaw-discovery' found")
	}
	rules, ok := role["rules"].([]any)
	if !ok || len(rules) == 0 {
		t.Fatal("frontend discovery ClusterRole has no rules")
	}

	for _, r := range rules {
		rm, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("frontend discovery ClusterRole has malformed rule %T", r)
		}
		if stringSet(rm["apiGroups"])[""] && stringSet(rm["resources"])["secrets"] {
			t.Fatalf("frontend discovery ClusterRole must not grant broad Secret access: %#v", rm)
		}
	}
}

func TestObolFrontendDiscoveryRBAC_ServiceOfferLeastPrivilege(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/obol-frontend.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}
	docs := multiDoc(data)

	role := findDocByName(docs, "ClusterRole", "obol-frontend-openclaw-discovery")
	if role == nil {
		t.Fatal("no ClusterRole 'obol-frontend-openclaw-discovery' found")
	}
	rules, ok := role["rules"].([]any)
	if !ok || len(rules) == 0 {
		t.Fatal("frontend discovery ClusterRole has no rules")
	}

	for _, required := range []string{"get", "list", "create", "patch"} {
		if !hasVerbOnResource(rules, "obol.org", "serviceoffers", required) {
			t.Fatalf("frontend ServiceOffer RBAC missing required verb %q", required)
		}
	}
	for _, forbidden := range []string{"update", "delete"} {
		if hasVerbOnResource(rules, "obol.org", "serviceoffers", forbidden) {
			t.Fatalf("frontend ServiceOffer RBAC grants forbidden verb %q", forbidden)
		}
	}
	for _, forbidden := range []string{"get", "list", "watch", "create", "update", "patch", "delete"} {
		if hasVerbOnResource(rules, "obol.org", "serviceoffers/status", forbidden) {
			t.Fatalf("frontend ServiceOffer RBAC grants status-subresource verb %q", forbidden)
		}
	}
}
