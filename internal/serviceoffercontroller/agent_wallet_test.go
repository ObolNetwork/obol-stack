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
	labels := secret.GetLabels()
	if labels["app.kubernetes.io/instance"] != agent.Name || labels["obol.org/agent"] != agent.Name {
		t.Errorf("agent ownership labels missing from remote-signer Secret: %+v", labels)
	}
	dataMap, _, _ := unstructured.NestedStringMap(secret.Object, "data")
	pwd, ok := dataMap["password"]
	if !ok || pwd == "" {
		t.Error("Secret missing 'password' key")
	}
	if decoded, err := base64.StdEncoding.DecodeString(pwd); err != nil || len(decoded) == 0 {
		t.Errorf("password not valid base64 / empty: %v", err)
	}
	keystoreValue, ok := dataMap[remoteSignerKeystoreKey]
	if !ok || keystoreValue == "" {
		t.Fatalf("Secret missing canonical %q key", remoteSignerKeystoreKey)
	}
	decodedKeystore, err := base64.StdEncoding.DecodeString(keystoreValue)
	if err != nil {
		t.Fatalf("%s not valid base64: %v", remoteSignerKeystoreKey, err)
	}
	if !strings.Contains(string(decodedKeystore), "\"crypto\"") {
		t.Fatalf("%s does not look like a V3 keystore JSON", remoteSignerKeystoreKey)
	}
	for k, v := range dataMap {
		if k == "password" {
			continue
		}
		if k != remoteSignerKeystoreKey && strings.HasSuffix(k, ".json") && v != "" {
			t.Errorf("unexpected extra keystore JSON key %q in fresh Secret", k)
		}
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
	secret := getRemoteSignerSecret(t, c, "agent-quant")
	labels := secret.GetLabels()
	if labels["app.kubernetes.io/instance"] != agent.Name || labels["obol.org/agent"] != agent.Name {
		t.Errorf("existing remote-signer Secret was not labeled for safe teardown: %+v", labels)
	}
	dataMap := remoteSignerSecretData(t, secret)
	wantData := remoteSignerSecretData(t, preSeeded)
	if dataMap[remoteSignerKeystoreKey] != wantData[remoteSignerKeystoreKey] {
		t.Error("canonical keystore data changed during reuse")
	}
	if dataMap["password"] != wantData["password"] {
		t.Error("keystore password changed during reuse")
	}

	address, err = c.ensureAgentWallet(context.Background(), agent)
	if err != nil {
		t.Fatalf("ensureAgentWallet second pass: %v", err)
	}
	if address != "0x1111111111111111111111111111111111111111" {
		t.Errorf("second address = %q, want pre-seeded value", address)
	}
	dataMap = remoteSignerSecretData(t, getRemoteSignerSecret(t, c, "agent-quant"))
	if dataMap[remoteSignerKeystoreKey] != wantData[remoteSignerKeystoreKey] {
		t.Error("canonical keystore data changed during second reuse")
	}
	if dataMap["password"] != wantData["password"] {
		t.Error("keystore password changed during second reuse")
	}
}

func TestEnsureAgentWallet_MigratesLegacyKeystoreKey(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	legacySecret := buildSignerKeystoreSecret("agent-quant", &openclaw.KeystoreMaterial{
		Address:      "0x1111111111111111111111111111111111111111",
		KeystoreUUID: "existing-uuid",
		KeystoreJSON: []byte(`{"crypto":{}}`),
		Password:     "preseed",
	})
	dataMap, found, err := unstructured.NestedStringMap(legacySecret.Object, "data")
	if err != nil {
		t.Fatalf("read generated secret data: %v", err)
	}
	if !found || dataMap[remoteSignerKeystoreKey] == "" {
		t.Fatalf("generated secret missing %q data: %v", remoteSignerKeystoreKey, dataMap)
	}
	dataMap["existing-uuid.json"] = dataMap[remoteSignerKeystoreKey]
	delete(dataMap, remoteSignerKeystoreKey)
	if err := unstructured.SetNestedStringMap(legacySecret.Object, dataMap, "data"); err != nil {
		t.Fatalf("set legacy data: %v", err)
	}
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"), legacySecret)

	address, err := c.ensureAgentWallet(context.Background(), agent)
	if err != nil {
		t.Fatalf("ensureAgentWallet: %v", err)
	}
	if address != "0x1111111111111111111111111111111111111111" {
		t.Errorf("address = %q, want pre-seeded value", address)
	}

	secret, err := c.client.Resource(monetizeapi.SecretGVR).Namespace("agent-quant").Get(context.Background(), remoteSignerSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	migrated, found, err := unstructured.NestedStringMap(secret.Object, "data")
	if err != nil {
		t.Fatalf("read migrated secret data: %v", err)
	}
	if !found {
		t.Fatal("migrated secret has no data")
	}
	if migrated[remoteSignerKeystoreKey] == "" {
		t.Fatalf("legacy keystore secret was not migrated to %q: keys=%v", remoteSignerKeystoreKey, migrated)
	}
	if migrated[remoteSignerKeystoreKey] != migrated["existing-uuid.json"] {
		t.Errorf("migrated keystore data differs from legacy key")
	}
	if migrated["password"] != dataMap["password"] {
		t.Error("migrated secret password changed")
	}
}

func TestEnsureAgentWallet_RejectsAmbiguousLegacyKeystoreKeys(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	legacySecret := buildSignerKeystoreSecret("agent-quant", &openclaw.KeystoreMaterial{
		Address:      "0x1111111111111111111111111111111111111111",
		KeystoreUUID: "existing-uuid",
		KeystoreJSON: []byte(`{"crypto":{"id":"first"}}`),
		Password:     "preseed",
	})
	dataMap := remoteSignerSecretData(t, legacySecret)
	dataMap["existing-uuid.json"] = dataMap[remoteSignerKeystoreKey]
	dataMap["other-uuid.json"] = base64.StdEncoding.EncodeToString([]byte(`{"crypto":{"id":"second"}}`))
	delete(dataMap, remoteSignerKeystoreKey)
	if err := unstructured.SetNestedStringMap(legacySecret.Object, dataMap, "data"); err != nil {
		t.Fatalf("set ambiguous legacy data: %v", err)
	}
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"), legacySecret)

	_, err := c.ensureAgentWallet(context.Background(), agent)
	if err == nil {
		t.Fatal("expected ambiguous legacy keystore error")
	}
	if !strings.Contains(err.Error(), "multiple legacy keystore JSON data keys") {
		t.Fatalf("error = %v, want ambiguous legacy keystore message", err)
	}
	secret := getRemoteSignerSecret(t, c, "agent-quant")
	after := remoteSignerSecretData(t, secret)
	if after[remoteSignerKeystoreKey] != "" {
		t.Fatal("ambiguous legacy secret should not get a canonical keystore key")
	}
	if resourceExists(t, c, "deployments", "agent-quant", remoteSignerName) {
		t.Fatal("remote-signer deployment should not be applied after ambiguous keystore error")
	}
}

func TestEnsureAgentWallet_RejectsExistingSecretWithoutAddressAnnotation(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	orphaned := buildSignerKeystoreSecret("agent-quant", &openclaw.KeystoreMaterial{
		Address:      "0x1111111111111111111111111111111111111111",
		KeystoreUUID: "existing-uuid",
		KeystoreJSON: []byte(`{"crypto":{}}`),
		Password:     "preseed",
	})
	orphaned.SetAnnotations(nil)
	wantData := remoteSignerSecretData(t, orphaned)
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"), orphaned)

	_, err := c.ensureAgentWallet(context.Background(), agent)
	if err == nil {
		t.Fatal("expected error for existing keystore Secret without address annotation")
	}
	if !strings.Contains(err.Error(), "exists without obol.org/wallet-address annotation") {
		t.Fatalf("error = %v, want missing address annotation message", err)
	}
	after := remoteSignerSecretData(t, getRemoteSignerSecret(t, c, "agent-quant"))
	if after[remoteSignerKeystoreKey] != wantData[remoteSignerKeystoreKey] {
		t.Error("orphaned keystore data changed despite missing annotation")
	}
	if after["password"] != wantData["password"] {
		t.Error("orphaned keystore password changed despite missing annotation")
	}
	if resourceExists(t, c, "deployments", "agent-quant", remoteSignerName) {
		t.Fatal("remote-signer deployment should not be applied after missing annotation error")
	}
}

func TestEnsureAgentWallet_RejectsAnnotatedSecretWithoutKeystoreJSON(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	broken := buildSignerKeystoreSecret("agent-quant", &openclaw.KeystoreMaterial{
		Address:      "0x1111111111111111111111111111111111111111",
		KeystoreUUID: "existing-uuid",
		KeystoreJSON: []byte(`{"crypto":{}}`),
		Password:     "preseed",
	})
	dataMap := remoteSignerSecretData(t, broken)
	delete(dataMap, remoteSignerKeystoreKey)
	if err := unstructured.SetNestedStringMap(broken.Object, dataMap, "data"); err != nil {
		t.Fatalf("set broken data: %v", err)
	}
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"), broken)

	_, err := c.ensureAgentWallet(context.Background(), agent)
	if err == nil {
		t.Fatal("expected missing keystore JSON error")
	}
	if !strings.Contains(err.Error(), "has wallet annotation but no keystore JSON data") {
		t.Fatalf("error = %v, want missing keystore JSON message", err)
	}
	after := remoteSignerSecretData(t, getRemoteSignerSecret(t, c, "agent-quant"))
	if after[remoteSignerKeystoreKey] != "" {
		t.Fatal("broken secret should not get an empty canonical keystore key")
	}
	if after["password"] != dataMap["password"] {
		t.Error("broken secret password changed despite migration failure")
	}
	if resourceExists(t, c, "deployments", "agent-quant", remoteSignerName) {
		t.Fatal("remote-signer deployment should not be applied after missing keystore JSON error")
	}
}

func TestEnsureAgentWallet_RejectsMalformedSecretData(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	malformed := buildSignerKeystoreSecret("agent-quant", &openclaw.KeystoreMaterial{
		Address:      "0x1111111111111111111111111111111111111111",
		KeystoreUUID: "existing-uuid",
		KeystoreJSON: []byte(`{"crypto":{}}`),
		Password:     "preseed",
	})
	malformed.Object["data"] = map[string]any{
		"password": float64(123),
	}
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"), malformed)

	_, err := c.ensureAgentWallet(context.Background(), agent)
	if err == nil {
		t.Fatal("expected malformed Secret data error")
	}
	if !strings.Contains(err.Error(), "read remote-signer-keystore data") {
		t.Fatalf("error = %v, want malformed Secret data message", err)
	}
	if resourceExists(t, c, "deployments", "agent-quant", remoteSignerName) {
		t.Fatal("remote-signer deployment should not be applied after malformed Secret data error")
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

func TestReconcileAgent_WithExistingWallet_DoesNotRotateKeyMaterial(t *testing.T) {
	agent := agentWithWallet(t, "quant-wallet", "agent-quant-wallet", true)
	preSeeded := buildSignerKeystoreSecret("agent-quant-wallet", &openclaw.KeystoreMaterial{
		Address:      "0x1111111111111111111111111111111111111111",
		KeystoreUUID: "existing-uuid",
		KeystoreJSON: []byte(`{"crypto":{"ciphertext":"preserved"}}`),
		Password:     "preseed",
	})
	wantData := remoteSignerSecretData(t, preSeeded)
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"), preSeeded)

	if err := c.reconcileAgent(context.Background(), "agent-quant-wallet/quant-wallet"); err != nil {
		t.Fatalf("reconcileAgent (finalizer): %v", err)
	}
	if err := c.reconcileAgent(context.Background(), "agent-quant-wallet/quant-wallet"); err != nil {
		t.Fatalf("reconcileAgent (provisioning): %v", err)
	}
	if err := c.reconcileAgent(context.Background(), "agent-quant-wallet/quant-wallet"); err != nil {
		t.Fatalf("reconcileAgent (idempotent): %v", err)
	}

	got := getAgent(t, c, "agent-quant-wallet", "quant-wallet")
	if got.Status.WalletAddress != "0x1111111111111111111111111111111111111111" {
		t.Errorf("walletAddress = %q, want existing keystore address", got.Status.WalletAddress)
	}
	after := remoteSignerSecretData(t, getRemoteSignerSecret(t, c, "agent-quant-wallet"))
	if after[remoteSignerKeystoreKey] != wantData[remoteSignerKeystoreKey] {
		t.Error("existing keystore data changed across reconcile lifecycle")
	}
	if after["password"] != wantData["password"] {
		t.Error("existing keystore password changed across reconcile lifecycle")
	}
}

func TestRemoteSignerManifests_ProjectOnlyCanonicalKeystoreAndUsePasswordEnv(t *testing.T) {
	agent := agentWithWallet(t, "quant", "agent-quant", true)
	manifests := remoteSignerManifests(agent)
	deployment := manifests[0]

	volumes, found, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "volumes")
	if err != nil || !found {
		t.Fatalf("deployment volumes not found: found=%v err=%v", found, err)
	}
	var keystoreVolume map[string]any
	for _, volume := range volumes {
		v, ok := volume.(map[string]any)
		if ok && v["name"] == "keystore" {
			keystoreVolume = v
			break
		}
	}
	if keystoreVolume == nil {
		t.Fatal("keystore volume not found")
	}
	secret, ok := keystoreVolume["secret"].(map[string]any)
	if !ok {
		t.Fatalf("keystore volume secret = %#v", keystoreVolume["secret"])
	}
	items, ok := secret["items"].([]any)
	if !ok {
		t.Fatalf("keystore volume items = %#v", secret["items"])
	}
	if len(items) != 1 {
		t.Fatalf("keystore volume items = %d, want 1", len(items))
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("keystore item = %#v", items[0])
	}
	if item["key"] != remoteSignerKeystoreKey || item["path"] != remoteSignerKeystoreKey {
		t.Fatalf("keystore projection = %#v, want key/path %q", item, remoteSignerKeystoreKey)
	}

	containers, found, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
	if err != nil || !found || len(containers) != 1 {
		t.Fatalf("deployment containers not found: len=%d found=%v err=%v", len(containers), found, err)
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatalf("container = %#v", containers[0])
	}
	env, ok := container["env"].([]any)
	if !ok {
		t.Fatalf("container env = %#v", container["env"])
	}
	passwordEnv := false
	for _, entry := range env {
		e, ok := entry.(map[string]any)
		if !ok || e["name"] != "SIGNER__KEYSTORE__PASSWORD" {
			continue
		}
		valueFrom, ok := e["valueFrom"].(map[string]any)
		if !ok {
			t.Fatalf("password env valueFrom = %#v", e["valueFrom"])
		}
		secretKeyRef, ok := valueFrom["secretKeyRef"].(map[string]any)
		if !ok {
			t.Fatalf("password env secretKeyRef = %#v", valueFrom["secretKeyRef"])
		}
		if secretKeyRef["name"] == remoteSignerSecretName && secretKeyRef["key"] == "password" {
			passwordEnv = true
		}
	}
	if !passwordEnv {
		t.Fatal("password must be supplied from the Secret password key via env")
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

func getRemoteSignerSecret(t *testing.T, c *Controller, namespace string) *unstructured.Unstructured {
	t.Helper()
	secret, err := c.client.Resource(monetizeapi.SecretGVR).Namespace(namespace).Get(context.Background(), remoteSignerSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get remote-signer Secret: %v", err)
	}
	return secret
}

func remoteSignerSecretData(t *testing.T, secret *unstructured.Unstructured) map[string]string {
	t.Helper()
	dataMap, found, err := unstructured.NestedStringMap(secret.Object, "data")
	if err != nil {
		t.Fatalf("read remote-signer Secret data: %v", err)
	}
	if !found {
		t.Fatal("remote-signer Secret has no data")
	}
	out := make(map[string]string, len(dataMap))
	for key, value := range dataMap {
		out[key] = value
	}
	return out
}
