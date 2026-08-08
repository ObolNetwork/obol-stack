package serviceoffercontroller

import (
	"os"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

// --- truncateMessage --------------------------------------------------------

func TestTruncateMessage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   \t\n  ", ""},
		{"short", "hello", "hello"},
		{"trims surrounding whitespace", "  hi  ", "hi"},
		{"200 chars unchanged", strings.Repeat("a", 200), strings.Repeat("a", 200)},
		{"201 chars truncated to 200", strings.Repeat("a", 201), strings.Repeat("a", 200)},
		{"500 chars truncated to 200", strings.Repeat("b", 500), strings.Repeat("b", 200)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateMessage(tt.in)
			if got != tt.want {
				t.Errorf("truncateMessage(%q) len=%d, want %q len=%d", tt.in, len(got), tt.want, len(tt.want))
			}
			if len(got) > 200 {
				t.Errorf("truncateMessage returned %d chars, must be <= 200", len(got))
			}
		})
	}
}

// --- getenvDefault ----------------------------------------------------------

func TestGetenvDefault(t *testing.T) {
	const key = "OBOL_TEST_GETENV_DEFAULT_KEY"
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	t.Run("unset returns fallback", func(t *testing.T) {
		_ = os.Unsetenv(key)
		if got := getenvDefault(key, "fallback"); got != "fallback" {
			t.Errorf("got %q, want fallback", got)
		}
	})
	t.Run("set returns value", func(t *testing.T) {
		t.Setenv(key, "actual")
		if got := getenvDefault(key, "fallback"); got != "actual" {
			t.Errorf("got %q, want actual", got)
		}
	})
	t.Run("whitespace-only value falls back", func(t *testing.T) {
		t.Setenv(key, "   \t ")
		if got := getenvDefault(key, "fallback"); got != "fallback" {
			t.Errorf("got %q, want fallback (whitespace should be treated as empty)", got)
		}
	})
}

// --- httpRouteAccepted ------------------------------------------------------

func TestHTTPRouteAccepted(t *testing.T) {
	tests := []struct {
		name  string
		route *unstructured.Unstructured
		want  bool
	}{
		{
			name: "no status",
			route: &unstructured.Unstructured{Object: map[string]any{
				"metadata": map[string]any{"name": "r"},
			}},
			want: false,
		},
		{
			name: "status with empty parents",
			route: &unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{"parents": []any{}},
			}},
			want: false,
		},
		{
			name: "parent with Accepted=True AND ResolvedRefs=True",
			route: &unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{
					"parents": []any{
						map[string]any{
							"conditions": []any{
								map[string]any{"type": "Accepted", "status": "True"},
								map[string]any{"type": "ResolvedRefs", "status": "True"},
							},
						},
					},
				},
			}},
			want: true,
		},
		{
			name: "Accepted=False",
			route: &unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{
					"parents": []any{
						map[string]any{
							"conditions": []any{
								map[string]any{"type": "Accepted", "status": "False"},
								map[string]any{"type": "ResolvedRefs", "status": "True"},
							},
						},
					},
				},
			}},
			want: false,
		},
		{
			name: "Accepted=True but ResolvedRefs=False",
			route: &unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{
					"parents": []any{
						map[string]any{
							"conditions": []any{
								map[string]any{"type": "Accepted", "status": "True"},
								map[string]any{"type": "ResolvedRefs", "status": "False"},
							},
						},
					},
				},
			}},
			want: false,
		},
		{
			name: "only Accepted condition, ResolvedRefs absent — not yet accepted (fail closed)",
			route: &unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{
					"parents": []any{
						map[string]any{
							"conditions": []any{
								map[string]any{"type": "Accepted", "status": "True"},
							},
						},
					},
				},
			}},
			want: false, // ResolvedRefs must be affirmatively True; absent is not trusted
		},
		{
			name: "multiple parents: first bad, second good",
			route: &unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{
					"parents": []any{
						map[string]any{
							"conditions": []any{
								map[string]any{"type": "Accepted", "status": "False"},
							},
						},
						map[string]any{
							"conditions": []any{
								map[string]any{"type": "Accepted", "status": "True"},
								map[string]any{"type": "ResolvedRefs", "status": "True"},
							},
						},
					},
				},
			}},
			want: true,
		},
		{
			name: "parent missing conditions slice",
			route: &unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{
					"parents": []any{
						map[string]any{"controllerName": "example/traefik"},
					},
				},
			}},
			want: false,
		},
		{
			// #767 route-acceptance gate: an UPDATE to an already-accepted
			// route must not be trusted until Traefik reconciles the new
			// spec. Status still reflects generation 1's verdict while the
			// route is now at generation 2.
			name: "Accepted=True but observedGeneration behind metadata.generation",
			route: &unstructured.Unstructured{Object: map[string]any{
				"metadata": map[string]any{"generation": int64(2)},
				"status": map[string]any{
					"parents": []any{
						map[string]any{
							"conditions": []any{
								map[string]any{"type": "Accepted", "status": "True", "observedGeneration": int64(1)},
								map[string]any{"type": "ResolvedRefs", "status": "True", "observedGeneration": int64(1)},
							},
						},
					},
				},
			}},
			want: false,
		},
		{
			name: "Accepted=True with observedGeneration matching metadata.generation",
			route: &unstructured.Unstructured{Object: map[string]any{
				"metadata": map[string]any{"generation": int64(2)},
				"status": map[string]any{
					"parents": []any{
						map[string]any{
							"conditions": []any{
								map[string]any{"type": "Accepted", "status": "True", "observedGeneration": int64(2)},
								map[string]any{"type": "ResolvedRefs", "status": "True", "observedGeneration": int64(2)},
							},
						},
					},
				},
			}},
			want: true,
		},
		{
			// Fail-closed: Accepted is current+True but ResolvedRefs is
			// stale (behind metadata.generation) and False. The stale
			// ResolvedRefs must NOT be ignored-as-true — the route has not
			// been resolved against the current spec.
			name: "Accepted current True but ResolvedRefs stale False",
			route: &unstructured.Unstructured{Object: map[string]any{
				"metadata": map[string]any{"generation": int64(2)},
				"status": map[string]any{
					"parents": []any{
						map[string]any{
							"conditions": []any{
								map[string]any{"type": "Accepted", "status": "True", "observedGeneration": int64(2)},
								map[string]any{"type": "ResolvedRefs", "status": "False", "observedGeneration": int64(1)},
							},
						},
					},
				},
			}},
			want: false,
		},
		{
			name: "Accepted current True but ResolvedRefs absent",
			route: &unstructured.Unstructured{Object: map[string]any{
				"metadata": map[string]any{"generation": int64(2)},
				"status": map[string]any{
					"parents": []any{
						map[string]any{
							"conditions": []any{
								map[string]any{"type": "Accepted", "status": "True", "observedGeneration": int64(2)},
							},
						},
					},
				},
			}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := httpRouteAccepted(tt.route); got != tt.want {
				t.Errorf("httpRouteAccepted = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- md5Sum -----------------------------------------------------------------

func TestMD5Sum_Deterministic(t *testing.T) {
	a := md5Sum("hello")
	b := md5Sum("hello")
	if a != b {
		t.Errorf("md5Sum non-deterministic: %x vs %x", a, b)
	}
	c := md5Sum("hello ")
	if a == c {
		t.Error("md5Sum should differ for different inputs")
	}

	// Length is always 16 bytes (compile-time [16]byte).
	if len(a) != 16 {
		t.Errorf("md5 digest length = %d, want 16", len(a))
	}
}

// --- requestCleanupComplete / requestPhaseReady -----------------------------

func TestRequestCleanupComplete(t *testing.T) {
	tests := []struct {
		phase string
		want  bool
	}{
		{registrationPhaseTombstoned, true},
		{registrationPhaseOffChainOnly, true},
		{registrationPhaseRegistered, false},
		{registrationPhasePublishing, false},
		{registrationPhaseRegistering, false},
		{registrationPhaseAwaitingExternal, false},
		{"", false},
		{"Unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.phase, func(t *testing.T) {
			if got := requestCleanupComplete(tt.phase); got != tt.want {
				t.Errorf("requestCleanupComplete(%q) = %v, want %v", tt.phase, got, tt.want)
			}
		})
	}
}

// --- firstNonEmpty ----------------------------------------------------------

func TestFirstNonEmpty_Controller(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"first wins", []string{"a", "b"}, "a"},
		{"whitespace skipped", []string{"  ", "b"}, "b"},
		{"trims result", []string{"  hello  "}, "hello"},
		{"all whitespace", []string{"", "  ", "\t"}, ""},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstNonEmpty(tt.in...); got != tt.want {
				t.Errorf("firstNonEmpty(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// --- decodeServiceOffer / decodeRegistrationRequest -------------------------

func TestDecodeServiceOffer_SetsUpstreamNamespaceDefault(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": monetizeapi.Group + "/" + monetizeapi.Version,
		"kind":       monetizeapi.ServiceOfferKind,
		"metadata":   map[string]any{"name": "demo", "namespace": "llm"},
		"spec": map[string]any{
			"upstream": map[string]any{
				"service": "ollama",
				"port":    int64(11434),
				// Intentionally no upstream.namespace — decoder must default to metadata.namespace.
			},
		},
	}}

	offer, err := decodeServiceOffer(u)
	if err != nil {
		t.Fatalf("decodeServiceOffer: %v", err)
	}
	if offer.Spec.Upstream.Namespace != "llm" {
		t.Errorf("upstream.namespace = %q, want llm (defaulted from metadata.namespace)", offer.Spec.Upstream.Namespace)
	}
}

func TestDecodeServiceOffer_KeepsExplicitUpstreamNamespace(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": monetizeapi.Group + "/" + monetizeapi.Version,
		"kind":       monetizeapi.ServiceOfferKind,
		"metadata":   map[string]any{"name": "demo", "namespace": "llm"},
		"spec": map[string]any{
			"upstream": map[string]any{
				"service":   "ollama",
				"namespace": "other-ns",
				"port":      int64(11434),
			},
		},
	}}

	offer, err := decodeServiceOffer(u)
	if err != nil {
		t.Fatalf("decodeServiceOffer: %v", err)
	}
	if offer.Spec.Upstream.Namespace != "other-ns" {
		t.Errorf("upstream.namespace = %q, want other-ns (explicit value must be preserved)", offer.Spec.Upstream.Namespace)
	}
}

func TestDecodeServiceOffer_MalformedReturnsError(t *testing.T) {
	// "spec" as a string breaks conversion into a struct.
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": monetizeapi.Group + "/" + monetizeapi.Version,
		"kind":       monetizeapi.ServiceOfferKind,
		"metadata":   map[string]any{"name": "bad", "namespace": "llm"},
		"spec":       "this should be an object",
	}}
	if _, err := decodeServiceOffer(u); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDecodeRegistrationRequest_Basic(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": monetizeapi.Group + "/" + monetizeapi.Version,
		"kind":       monetizeapi.RegistrationRequestKind,
		"metadata":   map[string]any{"name": "so-demo-registration", "namespace": "llm"},
		"spec": map[string]any{
			"serviceOfferName":      "demo",
			"serviceOfferNamespace": "llm",
			"desiredState":          registrationDesiredActive,
		},
	}}
	req, err := decodeRegistrationRequest(u)
	if err != nil {
		t.Fatalf("decodeRegistrationRequest: %v", err)
	}
	if req.Spec.ServiceOfferName != "demo" {
		t.Errorf("ServiceOfferName = %q, want demo", req.Spec.ServiceOfferName)
	}
	if req.Spec.DesiredState != registrationDesiredActive {
		t.Errorf("DesiredState = %q, want %q", req.Spec.DesiredState, registrationDesiredActive)
	}
}

// --- asUnstructured ---------------------------------------------------------

func TestAsUnstructured(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "x"}}}

	t.Run("plain unstructured pointer", func(t *testing.T) {
		got := asUnstructured(u)
		if got != u {
			t.Errorf("expected same pointer, got %p vs %p", got, u)
		}
	})

	t.Run("DeletedFinalStateUnknown wrapping unstructured", func(t *testing.T) {
		tombstone := cache.DeletedFinalStateUnknown{Key: "llm/x", Obj: u}
		got := asUnstructured(tombstone)
		if got != u {
			t.Errorf("expected to unwrap tombstone to %p, got %p", u, got)
		}
	})

	t.Run("DeletedFinalStateUnknown with non-unstructured inside returns nil", func(t *testing.T) {
		tombstone := cache.DeletedFinalStateUnknown{Key: "llm/x", Obj: "not an unstructured"}
		if got := asUnstructured(tombstone); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("unrelated type returns nil", func(t *testing.T) {
		if got := asUnstructured("a string"); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
		if got := asUnstructured(nil); got != nil {
			t.Errorf("expected nil for nil input, got %v", got)
		}
	})
}

// --- identity tombstone rendering -------------------------------------------

func TestBuildIdentityRegistrationDocument_Tombstone(t *testing.T) {
	identity := &monetizeapi.AgentIdentity{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "x402"},
	}

	doc := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: identity,
		BaseURL:  "https://example.com",
	})

	if doc.Active {
		t.Error("tombstone document must have Active=false")
	}
	if doc.X402Support {
		t.Error("tombstone must have X402Support=false")
	}
	if doc.Type != erc8004.RegistrationType {
		t.Errorf("Type = %q, want %q", doc.Type, erc8004.RegistrationType)
	}
	if !strings.Contains(doc.Description, "(deactivated)") {
		t.Errorf("description = %q, should contain (deactivated) suffix", doc.Description)
	}
}

// --- marshalRegistrationDocument --------------------------------------------

func TestMarshalRegistrationDocument(t *testing.T) {
	doc := erc8004.AgentRegistration{
		Type:        erc8004.RegistrationType,
		Name:        "Demo",
		Description: "Demo",
		Image:       "https://example.com/icon.png",
	}
	body, hash, err := marshalRegistrationDocument(doc)
	if err != nil {
		t.Fatalf("marshalRegistrationDocument: %v", err)
	}
	if body == "" {
		t.Error("expected non-empty body")
	}
	if len(hash) == 0 {
		t.Error("expected non-empty hash")
	}
	if !strings.Contains(body, `"name": "Demo"`) {
		t.Errorf("body missing pretty-printed name, got:\n%s", body)
	}

	// Identical input produces identical hash.
	_, hash2, _ := marshalRegistrationDocument(doc)
	if hash != hash2 {
		t.Errorf("hash non-deterministic: %q vs %q", hash, hash2)
	}

	// Changing content changes hash.
	doc.Description = "changed"
	_, hash3, _ := marshalRegistrationDocument(doc)
	if hash == hash3 {
		t.Error("hash should differ for different content")
	}
}

// --- selectRegistrationOwner additional cases -------------------------------

func TestSelectRegistrationOwner_ZeroTimestampsOrderedByName(t *testing.T) {
	// Both offers have zero CreationTimestamp — should fall back to ns/name order.
	a := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns1", UID: types.UID("1")}}
	b := &monetizeapi.ServiceOffer{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1", UID: types.UID("2")}}

	got := selectRegistrationOwner([]*monetizeapi.ServiceOffer{a, b})
	if got == nil {
		t.Fatal("expected non-nil winner")
	}
	if got.Name != "a" {
		t.Errorf("winner name = %q, want %q (name tiebreaker for equal zero timestamps)", got.Name, "a")
	}
}

func TestSelectRegistrationOwner_OneZeroTimestampLoses(t *testing.T) {
	withTime := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns1", CreationTimestamp: metav1.Now()},
	}
	zeroTime := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns1"},
	}
	got := selectRegistrationOwner([]*monetizeapi.ServiceOffer{zeroTime, withTime})
	if got == nil {
		t.Fatal("expected non-nil winner")
	}
	if got.Name != "a" {
		t.Errorf("zero timestamp should lose: winner = %q, want %q", got.Name, "a")
	}
}
