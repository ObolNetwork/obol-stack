package serviceoffercontroller

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestEnsureAgentWallet_FreshKeystore(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"))

	address, err := c.ensureAgentWallet(context.Background(), agent)
	if err != nil {
		t.Fatalf("ensureAgentWallet: %v", err)
	}
	if !strings.HasPrefix(address, "0x") || len(address) != 42 {
		t.Errorf("address looks malformed: %q", address)
	}

	// Secret must carry both the keystore JSON and the password, plus
	// the address as an annotation so subsequent reconciles can recover
	// the address without decrypting the keystore.
	secret, err := c.client.Resource(monetizeapi.SecretGVR).Namespace("agent-quant").Get(context.Background(), remoteSignerSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	annotations := secret.GetAnnotations()
	if annotations[signerKeystoreAddressAnnotation] != address {
		t.Errorf("address annotation = %q, want %q", annotations[signerKeystoreAddressAnnotation], address)
	}
	dataMap, _, _ := unstructured.NestedStringMap(secret.Object, "data")
	pwd, ok := dataMap["password"]
	if !ok || pwd == "" {
		t.Error("Secret missing 'password' key")
	}
	if decoded, err := base64.StdEncoding.DecodeString(pwd); err != nil || len(decoded) == 0 {
		t.Errorf("password not valid base64 / empty: %v", err)
	}
	hasKeystore := false
	for k, v := range dataMap {
		if k == "password" {
			continue
		}
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		if decoded, err := base64.StdEncoding.DecodeString(v); err == nil && strings.Contains(string(decoded), "\"crypto\"") {
			hasKeystore = true
			break
		}
	}
	if !hasKeystore {
		t.Error("Secret missing V3 keystore JSON")
	}

	// Deployment + Service got applied alongside.
	for _, kind := range []struct{ resource, name string }{
		{"deployments", remoteSignerName},
		{"services", remoteSignerName},
	} {
		if !resourceExists(t, c, kind.resource, "agent-quant", kind.name) {
			t.Errorf("%s/%s not applied", kind.resource, kind.name)
		}
	}
}

func TestEnsureAgentWallet_ReusesExistingKeystore(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	// Pre-seed a Secret with a known address — the controller must
	// recover that address rather than mint a fresh keypair.
	preSeeded := buildSignerKeystoreSecret("agent-quant", &openclaw.KeystoreMaterial{
		Address:      "0x1111111111111111111111111111111111111111",
		KeystoreUUID: "existing-uuid",
		KeystoreJSON: []byte(`{"crypto":{}}`),
		Password:     "preseed",
	})
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"), preSeeded)

	address, err := c.ensureAgentWallet(context.Background(), agent)
	if err != nil {
		t.Fatalf("ensureAgentWallet: %v", err)
	}
	if address != "0x1111111111111111111111111111111111111111" {
		t.Errorf("address = %q, want pre-seeded value", address)
	}
}

func TestEnsureAgentWallet_NoOpWhenWalletDisabled(t *testing.T) {
	agent := agentWithWallet(t, "no-wallet", "agent-no-wallet", false)
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"))

	address, err := c.ensureAgentWallet(context.Background(), agent)
	if err != nil {
		t.Fatalf("ensureAgentWallet: %v", err)
	}
	if address != "" {
		t.Errorf("address = %q, want empty when wallet disabled", address)
	}
	// No remote-signer Secret should have landed.
	if _, err := c.client.Resource(monetizeapi.SecretGVR).Namespace("agent-no-wallet").Get(context.Background(), remoteSignerSecretName, metav1.GetOptions{}); err == nil {
		t.Error("remote-signer Secret unexpectedly created when wallet.create=false")
	}
}

func TestReconcileAgent_WithWallet_PopulatesAddressAndReady(t *testing.T) {
	agent := agentWithWallet(t, "quant-wallet", "agent-quant-wallet", true)
	agent.Spec.Model = "qwen3.5:9b"

	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"))

	// First reconcile lands the finalizer; second reconcile provisions
	// Hermes + remote-signer and populates status.walletAddress. The
	// fake client never reports readyReplicas so we still expect
	// Phase=Provisioning at the end.
	if err := c.reconcileAgent(context.Background(), "agent-quant-wallet/quant-wallet"); err != nil {
		t.Fatalf("reconcileAgent (finalizer): %v", err)
	}
	if err := c.reconcileAgent(context.Background(), "agent-quant-wallet/quant-wallet"); err != nil {
		t.Fatalf("reconcileAgent (provisioning): %v", err)
	}
	got := getAgent(t, c, "agent-quant-wallet", "quant-wallet")
	if got.Status.WalletAddress == "" || !strings.HasPrefix(got.Status.WalletAddress, "0x") {
		t.Errorf("walletAddress not populated: %q", got.Status.WalletAddress)
	}
	if got.Status.Phase != monetizeapi.AgentPhaseProvisioning {
		t.Errorf("phase = %q, want Provisioning (no kubelet)", got.Status.Phase)
	}
}

// agentWithWallet builds an Agent CR for the wallet tests with a model
// set so we don't trip the ModelUnpinned guard before reaching the
// wallet path.
func agentWithWallet(t *testing.T, name, namespace string, create bool) *monetizeapi.Agent {
	t.Helper()
	return &monetizeapi.Agent{
		TypeMeta:   metav1.TypeMeta{APIVersion: "obol.org/v1alpha1", Kind: "Agent"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: monetizeapi.AgentSpec{
			Runtime: "hermes",
			Model:   "qwen3.5:9b",
			Skills:  []string{"addresses"},
			Wallet:  monetizeapi.AgentWallet{Create: create},
		},
	}
}
