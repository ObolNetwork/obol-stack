package serviceoffercontroller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestEnsureAgentWallet_FreshSecretCarriesAuthToken: new keystores mint a
// bearer token alongside the key material so the signer enforces auth from
// first boot (once the signer image pin supports it).
func TestEnsureAgentWallet_FreshSecretCarriesAuthToken(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"))

	if _, err := c.ensureAgentWallet(context.Background(), agent); err != nil {
		t.Fatalf("ensureAgentWallet: %v", err)
	}
	data := remoteSignerSecretData(t, getRemoteSignerSecret(t, c, "agent-quant"))
	if data[remoteSignerAuthTokenKey] == "" {
		t.Fatalf("fresh keystore Secret missing %q key", remoteSignerAuthTokenKey)
	}
}

// TestEnsureAgentWallet_BackfillsAuthTokenWithoutTouchingKeyMaterial: a
// pre-auth Secret (minted before signer auth existed) gains the token on
// the next reconcile, and ONLY the token — keystore and password are
// untouched, and the token never rotates on later reconciles.
func TestEnsureAgentWallet_BackfillsAuthTokenWithoutTouchingKeyMaterial(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	preAuth := buildSignerKeystoreSecret("agent-quant", &openclaw.KeystoreMaterial{
		Address:      "0x1111111111111111111111111111111111111111",
		KeystoreUUID: "existing-uuid",
		KeystoreJSON: []byte(`{"crypto":{"ciphertext":"preserved"}}`),
		Password:     "preseed",
	}, "ignored")
	unstructured.RemoveNestedField(preAuth.Object, "data", remoteSignerAuthTokenKey)
	ensureRemoteSignerSecretLabels(preAuth, agent.Name)
	want := remoteSignerSecretData(t, preAuth)
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"), preAuth)

	if _, err := c.ensureAgentWallet(context.Background(), agent); err != nil {
		t.Fatalf("ensureAgentWallet: %v", err)
	}
	after := remoteSignerSecretData(t, getRemoteSignerSecret(t, c, "agent-quant"))
	token := after[remoteSignerAuthTokenKey]
	if token == "" {
		t.Fatal("auth token not backfilled onto pre-auth Secret")
	}
	if after[remoteSignerKeystoreKey] != want[remoteSignerKeystoreKey] || after["password"] != want["password"] {
		t.Error("backfill must not touch keystore/password data")
	}

	// Second reconcile: token is stable (no rotation).
	if _, err := c.ensureAgentWallet(context.Background(), agent); err != nil {
		t.Fatalf("ensureAgentWallet second pass: %v", err)
	}
	again := remoteSignerSecretData(t, getRemoteSignerSecret(t, c, "agent-quant"))
	if again[remoteSignerAuthTokenKey] != token {
		t.Error("auth token rotated across reconciles — clients would race the rotation")
	}
}

// TestSignerAuthEnvInjection: the signer Deployment reads the token as
// SIGNER__AUTH__TOKEN and the Hermes Deployment as REMOTE_SIGNER_TOKEN,
// both via optional secretKeyRefs (pre-backfill Secrets and wallet-less
// agents must not wedge pod startup).
func TestSignerAuthEnvInjection(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	agent.Spec.Model = "qwen3.5:9b"

	signerDeploy := remoteSignerManifests(agent)[0]
	assertOptionalTokenEnv(t, signerDeploy, "SIGNER__AUTH__TOKEN")

	manifests, err := agentManifests(agent, "litellm-key", "api-key")
	if err != nil {
		t.Fatalf("agentManifests: %v", err)
	}
	var hermesDeploy *unstructured.Unstructured
	for _, m := range manifests {
		if m.GetKind() == "Deployment" {
			hermesDeploy = m
		}
	}
	assertOptionalTokenEnv(t, hermesDeploy, "REMOTE_SIGNER_TOKEN")
}

func assertOptionalTokenEnv(t *testing.T, deploy *unstructured.Unstructured, envName string) {
	t.Helper()
	raw, err := json.Marshal(deploy.Object)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"name":"`+envName+`"`) {
		t.Fatalf("%s env not wired on %s", envName, deploy.GetName())
	}
	idx := strings.Index(body, `"name":"`+envName+`"`)
	window := body[idx:min(idx+400, len(body))]
	if !strings.Contains(window, `"key":"`+remoteSignerAuthTokenKey+`"`) {
		t.Errorf("%s must reference Secret key %q: %s", envName, remoteSignerAuthTokenKey, window)
	}
	if !strings.Contains(window, `"optional":true`) {
		t.Errorf("%s secretKeyRef must be optional (pre-backfill Secrets / older clusters): %s", envName, window)
	}
}
