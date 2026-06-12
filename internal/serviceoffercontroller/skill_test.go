package serviceoffercontroller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
)

// newSkillTestController builds a Controller wired with the GVRs that
// reconcileSkillBundle touches, backed by the fake dynamic client (same
// harness style as newProvisioningTestController).
func newSkillTestController(t *testing.T, seedObjects ...*unstructured.Unstructured) *Controller {
	t.Helper()

	objects := make([]runtime.Object, 0, len(seedObjects))
	for _, o := range seedObjects {
		objects = append(objects, o)
	}

	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			monetizeapi.ConfigMapGVR:  "ConfigMapList",
			monetizeapi.ServiceGVR:    "ServiceList",
			monetizeapi.DeploymentGVR: "DeploymentList",
		},
		objects...,
	)

	return &Controller{
		dynClient:   dynClient,
		client:      dynClient,
		services:    dynClient.Resource(monetizeapi.ServiceGVR),
		configMaps:  dynClient.Resource(monetizeapi.ConfigMapGVR),
		deployments: dynClient.Resource(monetizeapi.DeploymentGVR),
	}
}

// skillTestBundle is a stand-in for gzipped tar bytes; reconcileSkillBundle
// only hashes and measures them, it never unpacks.
var skillTestBundle = []byte("fake-gzipped-skill-bundle-bytes")

func skillTestBundleHash() string {
	sum := sha256.Sum256(skillTestBundle)
	return hex.EncodeToString(sum[:])
}

// skillTestOffer returns a well-formed type=skill offer whose upstream is
// pinned to the controller-rendered bundle server, exactly as the CLI
// writes it. mutate lets each table case break one thing.
func skillTestOffer(mutate func(*monetizeapi.ServiceOffer)) *monetizeapi.ServiceOffer {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "buy-x402",
			Namespace: "hermes-obol-agent",
			UID:       types.UID("offer-uid-1"),
		},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "skill",
			Skill: monetizeapi.ServiceOfferSkill{
				Name:            "buy-x402",
				Version:         "0.1.0",
				SHA256:          skillTestBundleHash(),
				BundleConfigMap: "buy-x402-skill-bundle",
				DisplayName:     "Buy x402",
				Description:     "Pre-sign x402 payment authorizations",
			},
			Upstream: monetizeapi.ServiceOfferUpstream{
				Service:    monetizeapi.SkillBundleWorkloadName("buy-x402"),
				Namespace:  "hermes-obol-agent",
				Port:       8080,
				HealthPath: "/skill.json",
			},
			Payment: monetizeapi.ServiceOfferPayment{
				PayTo:   "0x1111111111111111111111111111111111111111",
				Network: "base-sepolia",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.01"},
			},
		},
	}
	if mutate != nil {
		mutate(offer)
	}
	return offer
}

// bundleConfigMapObject renders the operator-supplied bundle ConfigMap the
// way the apiserver stores it: binaryData values base64-encoded.
func bundleConfigMapObject(namespace, name string, payload []byte) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"binaryData": map[string]any{
			monetizeapi.SkillBundleKey: base64.StdEncoding.EncodeToString(payload),
		},
	}}
}

func conditionByType(status monetizeapi.ServiceOfferStatus, conditionType string) *monetizeapi.Condition {
	for i := range status.Conditions {
		if status.Conditions[i].Type == conditionType {
			return &status.Conditions[i]
		}
	}
	return nil
}

func TestReconcileSkillBundle_FailureTable(t *testing.T) {
	oversize := make([]byte, monetizeapi.MaxSkillBundleBytes+1)

	cases := []struct {
		name       string
		mutate     func(*monetizeapi.ServiceOffer)
		seed       []*unstructured.Unstructured
		wantReason string
	}{
		{
			name:       "missing bundle ConfigMap",
			seed:       nil,
			wantReason: "BundleMissing",
		},
		{
			name: "ConfigMap without binaryData key",
			seed: []*unstructured.Unstructured{{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]any{
					"name":      "buy-x402-skill-bundle",
					"namespace": "hermes-obol-agent",
				},
				"data": map[string]any{"unrelated": "value"},
			}}},
			wantReason: "BundleMissing",
		},
		{
			name: "bundle exceeds MaxSkillBundleBytes",
			seed: []*unstructured.Unstructured{
				bundleConfigMapObject("hermes-obol-agent", "buy-x402-skill-bundle", oversize),
			},
			wantReason: "BundleTooLarge",
		},
		{
			name: "sha256 mismatch",
			mutate: func(o *monetizeapi.ServiceOffer) {
				o.Spec.Skill.SHA256 = strings.Repeat("ab", 32)
			},
			seed: []*unstructured.Unstructured{
				bundleConfigMapObject("hermes-obol-agent", "buy-x402-skill-bundle", skillTestBundle),
			},
			wantReason: "BundleHashMismatch",
		},
		{
			name: "spoofed upstream service",
			mutate: func(o *monetizeapi.ServiceOffer) {
				o.Spec.Upstream.Service = "litellm"
			},
			seed: []*unstructured.Unstructured{
				bundleConfigMapObject("hermes-obol-agent", "buy-x402-skill-bundle", skillTestBundle),
			},
			wantReason: "InvalidSkillUpstream",
		},
		{
			name: "spoofed upstream namespace",
			mutate: func(o *monetizeapi.ServiceOffer) {
				o.Spec.Upstream.Namespace = "llm"
			},
			seed: []*unstructured.Unstructured{
				bundleConfigMapObject("hermes-obol-agent", "buy-x402-skill-bundle", skillTestBundle),
			},
			wantReason: "InvalidSkillUpstream",
		},
		{
			name: "spoofed upstream port",
			mutate: func(o *monetizeapi.ServiceOffer) {
				o.Spec.Upstream.Port = 4000
			},
			seed: []*unstructured.Unstructured{
				bundleConfigMapObject("hermes-obol-agent", "buy-x402-skill-bundle", skillTestBundle),
			},
			wantReason: "InvalidSkillUpstream",
		},
		{
			name: "missing required skill fields",
			mutate: func(o *monetizeapi.ServiceOffer) {
				o.Spec.Skill.SHA256 = ""
			},
			wantReason: "InvalidSkillSpec",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newSkillTestController(t, tc.seed...)
			offer := skillTestOffer(tc.mutate)
			status := monetizeapi.ServiceOfferStatus{}

			ok, err := c.reconcileSkillBundle(context.Background(), &status, offer)
			if err != nil {
				t.Fatalf("reconcileSkillBundle: %v", err)
			}
			if ok {
				t.Fatal("ok = true, want false")
			}

			cond := conditionByType(status, "UpstreamHealthy")
			if cond == nil {
				t.Fatalf("UpstreamHealthy condition not set: %+v", status.Conditions)
			}
			if cond.Status != "False" {
				t.Errorf("UpstreamHealthy = %q, want False", cond.Status)
			}
			if cond.Reason != tc.wantReason {
				t.Errorf("UpstreamHealthy reason = %q, want %q (message: %s)", cond.Reason, tc.wantReason, cond.Message)
			}

			// No children may be published when validation fails.
			workload := monetizeapi.SkillBundleWorkloadName(offer.Name)
			if resourceExists(t, c, "deployments", offer.Namespace, workload) {
				t.Error("bundle Deployment must not be created on a failed validation")
			}
			if resourceExists(t, c, "services", offer.Namespace, workload) {
				t.Error("bundle Service must not be created on a failed validation")
			}
			if resourceExists(t, c, "configmaps", offer.Namespace, skillBundleMetaName(offer.Name)) {
				t.Error("meta ConfigMap must not be created on a failed validation")
			}
		})
	}
}

func TestReconcileSkillBundle_HappyPathAppliesChildren(t *testing.T) {
	c := newSkillTestController(t,
		bundleConfigMapObject("hermes-obol-agent", "buy-x402-skill-bundle", skillTestBundle),
	)
	offer := skillTestOffer(nil)
	status := monetizeapi.ServiceOfferStatus{}

	ok, err := c.reconcileSkillBundle(context.Background(), &status, offer)
	if err != nil {
		t.Fatalf("reconcileSkillBundle: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true; conditions: %+v", status.Conditions)
	}

	// reconcileSkillBundle must NOT claim UpstreamHealthy itself — the
	// shared reconcileUpstream health check owns that verdict.
	if cond := conditionByType(status, "UpstreamHealthy"); cond != nil {
		t.Errorf("UpstreamHealthy should be left to reconcileUpstream, got %+v", cond)
	}

	workload := monetizeapi.SkillBundleWorkloadName(offer.Name)
	ctx := context.Background()

	dep, err := c.deployments.Namespace(offer.Namespace).Get(ctx, workload, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("bundle Deployment missing: %v", err)
	}
	owners := dep.GetOwnerReferences()
	if len(owners) != 1 || owners[0].Kind != monetizeapi.ServiceOfferKind || owners[0].Name != offer.Name {
		t.Errorf("Deployment ownerReferences = %+v, want single ServiceOffer/%s owner", owners, offer.Name)
	}
	hash, _, _ := unstructured.NestedString(dep.Object, "spec", "template", "metadata", "annotations", "obol.org/content-hash")
	if want := skillTestBundleHash()[:8]; hash != want {
		t.Errorf("content-hash annotation = %q, want %q", hash, want)
	}

	if _, err := c.services.Namespace(offer.Namespace).Get(ctx, workload, metav1.GetOptions{}); err != nil {
		t.Fatalf("bundle Service missing: %v", err)
	}
	meta, err := c.configMaps.Namespace(offer.Namespace).Get(ctx, skillBundleMetaName(offer.Name), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("meta ConfigMap missing: %v", err)
	}
	skillJSON, _, _ := unstructured.NestedString(meta.Object, "data", "skill.json")
	for _, want := range []string{`"name": "buy-x402"`, `"version": "0.1.0"`, skillTestBundleHash()} {
		if !strings.Contains(skillJSON, want) {
			t.Errorf("skill.json missing %q:\n%s", want, skillJSON)
		}
	}
}

func TestReconcileSkillBundle_HashCompareIsCaseInsensitive(t *testing.T) {
	c := newSkillTestController(t,
		bundleConfigMapObject("hermes-obol-agent", "buy-x402-skill-bundle", skillTestBundle),
	)
	offer := skillTestOffer(func(o *monetizeapi.ServiceOffer) {
		o.Spec.Skill.SHA256 = strings.ToUpper(skillTestBundleHash())
	})
	status := monetizeapi.ServiceOfferStatus{}

	ok, err := c.reconcileSkillBundle(context.Background(), &status, offer)
	if err != nil {
		t.Fatalf("reconcileSkillBundle: %v", err)
	}
	if !ok {
		t.Fatalf("uppercase spec hash must still match (CRD enforces lowercase, controller stays lenient); conditions: %+v", status.Conditions)
	}
}

func TestReconcileSkillBundle_RepublishedBundleRollsContentHash(t *testing.T) {
	c := newSkillTestController(t,
		bundleConfigMapObject("hermes-obol-agent", "buy-x402-skill-bundle", skillTestBundle),
	)
	offer := skillTestOffer(nil)
	ctx := context.Background()

	status := monetizeapi.ServiceOfferStatus{}
	if ok, err := c.reconcileSkillBundle(ctx, &status, offer); err != nil || !ok {
		t.Fatalf("first reconcile: ok=%v err=%v", ok, err)
	}

	// Operator re-publishes a new bundle: CM bytes + spec hash both move.
	newBundle := []byte("v2-bundle-bytes")
	newSum := sha256.Sum256(newBundle)
	newHash := hex.EncodeToString(newSum[:])
	if _, err := c.configMaps.Namespace(offer.Namespace).Update(ctx,
		bundleConfigMapObject(offer.Namespace, "buy-x402-skill-bundle", newBundle), metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update bundle CM: %v", err)
	}
	offer.Spec.Skill.SHA256 = newHash
	offer.Spec.Skill.Version = "0.2.0"

	status = monetizeapi.ServiceOfferStatus{}
	if ok, err := c.reconcileSkillBundle(ctx, &status, offer); err != nil || !ok {
		t.Fatalf("second reconcile: ok=%v err=%v conditions=%+v", ok, err, status.Conditions)
	}

	dep, err := c.deployments.Namespace(offer.Namespace).Get(ctx, monetizeapi.SkillBundleWorkloadName(offer.Name), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("bundle Deployment missing after re-publish: %v", err)
	}
	hash, _, _ := unstructured.NestedString(dep.Object, "spec", "template", "metadata", "annotations", "obol.org/content-hash")
	if want := newHash[:8]; hash != want {
		t.Errorf("content-hash after re-publish = %q, want %q (pod must roll)", hash, want)
	}
}
