package serviceoffercontroller

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Constants for the per-agent remote-signer side-stack. Image pinned by
// digest is desirable but the chart still publishes by tag — keeping the
// version synced with agentruntime.RemoteSignerChartVersion's notes
// (chart 0.3.3 → image v0.4.0, the first image that honours
// SIGNER__AUTH__TOKEN).
const (
	remoteSignerName  = "remote-signer"
	remoteSignerPort  = 9000
	remoteSignerImage = "ghcr.io/obolnetwork/remote-signer:v0.4.0"
	// Image hard-codes /data/keystores as the default and reads its
	// config under the SIGNER__... env namespace; values picked to match
	// the master agent's working config in hermes-obol-agent.
	remoteSignerKeystoreDir = "/data/keystores"
	remoteSignerSecretName  = "remote-signer-keystore"
	// Fixed filename for the keystore inside the Secret + projected
	// volume. The remote-signer reads the address from inside the V3
	// keystore document, so the on-disk name is purely cosmetic — a
	// stable name lets the volume `items` projection drop the password
	// key cleanly without us having to thread the UUID through.
	remoteSignerKeystoreKey = "keystore.json"
	// Bearer token for the signer's REST API. Injected into the signer
	// as SIGNER__AUTH__TOKEN and into Hermes as REMOTE_SIGNER_TOKEN —
	// defense-in-depth on top of the agent-isolation NetworkPolicy.
	// Honoured by signer image v0.4.0+ (the version chart 0.3.3 ships);
	// older images silently ignore the env.
	remoteSignerAuthTokenKey = "authToken"
)

// ensureAgentWallet provisions a per-namespace remote-signer when the
// Agent has wallet.create=true. It mints a fresh Ethereum keypair (in
// memory; the keystore lands in a K8s Secret rather than a host PVC),
// applies the remote-signer Deployment + Service, and returns the
// agent's wallet address so the caller can populate Agent.status.
//
// Idempotent: if a Secret named remote-signer-keystore already exists
// in the namespace, we trust its address annotation rather than minting
// a new keypair on every reconcile. That keeps the agent's identity
// stable across controller restarts and Deployment rolls.
func (c *Controller) ensureAgentWallet(ctx context.Context, agent *monetizeapi.Agent) (string, error) {
	if !agent.Spec.Wallet.Create {
		return "", nil
	}

	address, err := c.ensureSignerKeystore(ctx, agent)
	if err != nil {
		return "", fmt.Errorf("ensure keystore: %w", err)
	}

	manifests := remoteSignerManifests(agent)
	for _, m := range manifests {
		if err := c.applyAgentObject(ctx, c.resourceFor(m), m); err != nil {
			return "", fmt.Errorf("apply %s/%s: %w", m.GetKind(), m.GetName(), err)
		}
	}

	return address, nil
}

// signerKeystoreAddressAnnotation pins the wallet's address on the
// keystore Secret so subsequent reconciles can recover it without
// decrypting the keystore. Annotation rather than data key because
// the address isn't sensitive — anyone in the namespace can read it
// off the on-chain ERC-8004 registration anyway.
const signerKeystoreAddressAnnotation = "obol.org/wallet-address"

// ensureSignerKeystore is the keystore-side half of ensureAgentWallet.
// Either reads an existing keystore Secret's address annotation, or
// mints a new keypair, encrypts to V3, and writes the Secret.
func (c *Controller) ensureSignerKeystore(ctx context.Context, agent *monetizeapi.Agent) (string, error) {
	namespace := agent.Namespace
	existing, err := c.client.Resource(monetizeapi.SecretGVR).Namespace(namespace).Get(ctx, remoteSignerSecretName, metav1.GetOptions{})
	if err == nil {
		annotations := existing.GetAnnotations()
		if addr := annotations[signerKeystoreAddressAnnotation]; addr != "" {
			if err := c.backfillSignerAuthToken(ctx, namespace, existing); err != nil {
				return "", fmt.Errorf("backfill signer auth token: %w", err)
			}
			return addr, nil
		}
		// Secret exists but has no address — likely written by a
		// non-controller actor. Don't clobber it; surface the gap so
		// operators can investigate rather than silently accepting a
		// half-formed keystore.
		return "", fmt.Errorf("secret %s/%s exists without %s annotation", namespace, remoteSignerSecretName, signerKeystoreAddressAnnotation)
	}
	if !apierrors.IsNotFound(err) {
		return "", err
	}

	mat, err := openclaw.GenerateKeystoreInMemory()
	if err != nil {
		return "", err
	}
	authToken, err := generateAPIKey()
	if err != nil {
		return "", err
	}

	secret := buildSignerKeystoreSecret(namespace, mat, authToken)
	ensureRemoteSignerSecretLabels(secret, agent.Name)
	if err := c.applyAgentObject(ctx, c.client.Resource(monetizeapi.SecretGVR).Namespace(namespace), secret); err != nil {
		return "", err
	}
	return mat.Address, nil
}

// backfillSignerAuthToken adds the bearer-token key to keystore Secrets
// minted before signer auth existed. One-shot per Secret: presence of the
// key (even an empty value) means we leave it alone, so operator-rotated
// tokens are never clobbered by reconciles.
func (c *Controller) backfillSignerAuthToken(ctx context.Context, namespace string, existing *unstructured.Unstructured) error {
	data, _, _ := unstructured.NestedMap(existing.Object, "data")
	if _, ok := data[remoteSignerAuthTokenKey]; ok {
		return nil
	}
	token, err := generateAPIKey()
	if err != nil {
		return err
	}
	updated := existing.DeepCopy()
	if err := unstructured.SetNestedField(updated.Object,
		base64.StdEncoding.EncodeToString([]byte(token)),
		"data", remoteSignerAuthTokenKey); err != nil {
		return err
	}
	_, err = c.client.Resource(monetizeapi.SecretGVR).Namespace(namespace).Update(ctx, updated, metav1.UpdateOptions{})
	return err
}

func ensureRemoteSignerSecretLabels(secret *unstructured.Unstructured, agentName string) bool {
	labels := secret.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	changed := false
	for key, value := range map[string]string{
		"app.kubernetes.io/name":       remoteSignerName,
		"app.kubernetes.io/managed-by": "serviceoffer-controller",
		"app.kubernetes.io/instance":   agentName,
		"obol.org/agent":               agentName,
	} {
		if labels[key] != value {
			labels[key] = value
			changed = true
		}
	}
	if changed {
		secret.SetLabels(labels)
	}
	return changed
}

func buildSignerKeystoreSecret(namespace string, mat *openclaw.KeystoreMaterial, authToken string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      remoteSignerSecretName,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/name":       remoteSignerName,
				"app.kubernetes.io/managed-by": "serviceoffer-controller",
			},
			"annotations": map[string]any{
				signerKeystoreAddressAnnotation: mat.Address,
			},
		},
		"type": "Opaque",
		"data": map[string]any{
			// Fixed key name; the V3 document carries the UUID + address
			// internally, and the volume's `items` projection only
			// references this key (the password lives under a separate
			// key, read via env, never mounted into the keystore dir).
			remoteSignerKeystoreKey:  base64.StdEncoding.EncodeToString(mat.KeystoreJSON),
			"password":               base64.StdEncoding.EncodeToString([]byte(mat.Password)),
			remoteSignerAuthTokenKey: base64.StdEncoding.EncodeToString([]byte(authToken)),
		},
	})
	return u
}

// remoteSignerManifests returns the Deployment + Service that back the
// per-agent remote-signer. Skips the keystore Secret — that's owned by
// ensureSignerKeystore above so existing keystores survive reconciles.
func remoteSignerManifests(agent *monetizeapi.Agent) []*unstructured.Unstructured {
	labels := map[string]string{
		"app.kubernetes.io/name":       remoteSignerName,
		"app.kubernetes.io/managed-by": "serviceoffer-controller",
		"app.kubernetes.io/instance":   agent.Name,
	}

	deployment := &unstructured.Unstructured{}
	deployment.SetUnstructuredContent(map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      remoteSignerName,
			"namespace": agent.Namespace,
			"labels":    asAnyMap(labels),
		},
		"spec": map[string]any{
			"replicas": int64(1),
			"selector": map[string]any{
				"matchLabels": map[string]any{
					"app.kubernetes.io/name":     remoteSignerName,
					"app.kubernetes.io/instance": agent.Name,
				},
			},
			"template": map[string]any{
				"metadata": map[string]any{"labels": asAnyMap(labels)},
				"spec": map[string]any{
					"securityContext": map[string]any{
						// Remote-signer image runs as UID 1000; fsGroup
						// makes the mounted Secret readable by the pod
						// without an init container chowning files.
						"fsGroup": int64(1000),
					},
					"containers": []any{
						map[string]any{
							"name":            remoteSignerName,
							"image":           remoteSignerImage,
							"imagePullPolicy": "IfNotPresent",
							"ports": []any{
								map[string]any{"name": "http", "containerPort": int64(remoteSignerPort)},
							},
							// Env names match the upstream remote-signer image's
							// SIGNER__<SECTION>__<KEY> hierarchy. Mirrors the
							// master agent's config in hermes-obol-agent.
							"env": []any{
								map[string]any{"name": "SIGNER__SERVER__HOST", "value": "0.0.0.0"},
								map[string]any{"name": "SIGNER__SERVER__PORT", "value": fmt.Sprintf("%d", remoteSignerPort)},
								map[string]any{"name": "SIGNER__KEYSTORE__DIR", "value": remoteSignerKeystoreDir},
								map[string]any{"name": "SIGNER__LOGGING__FORMAT", "value": "json"},
								map[string]any{"name": "SIGNER__LOGGING__LEVEL", "value": "info"},
								map[string]any{
									"name": "SIGNER__KEYSTORE__PASSWORD",
									"valueFrom": map[string]any{
										"secretKeyRef": map[string]any{
											"name": remoteSignerSecretName,
											"key":  "password",
										},
									},
								},
								// optional: pre-auth Secrets may lack the key
								// until the controller backfills it; the pod
								// must not deadlock on that (signer treats an
								// absent token as auth-disabled).
								map[string]any{
									"name": "SIGNER__AUTH__TOKEN",
									"valueFrom": map[string]any{
										"secretKeyRef": map[string]any{
											"name":     remoteSignerSecretName,
											"key":      remoteSignerAuthTokenKey,
											"optional": true,
										},
									},
								},
							},
							"readinessProbe": map[string]any{
								"httpGet":             map[string]any{"path": "/healthz", "port": int64(remoteSignerPort)},
								"initialDelaySeconds": int64(2),
								"periodSeconds":       int64(5),
							},
							"livenessProbe": map[string]any{
								"httpGet":             map[string]any{"path": "/healthz", "port": int64(remoteSignerPort)},
								"initialDelaySeconds": int64(10),
								"periodSeconds":       int64(15),
							},
							"volumeMounts": []any{
								map[string]any{
									"name":      "keystore",
									"mountPath": remoteSignerKeystoreDir,
									"readOnly":  true,
								},
							},
						},
					},
					"volumes": []any{
						map[string]any{
							"name": "keystore",
							"secret": map[string]any{
								"secretName": remoteSignerSecretName,
								// Mount only the keystore JSON. The password
								// lives in the same Secret under a separate
								// key but is read via env, not file —
								// projecting it would create a non-keystore
								// file in /data/keystores and trigger the
								// signer's "skipping keystore" warnings.
								"items": []any{
									map[string]any{"key": remoteSignerKeystoreKey, "path": remoteSignerKeystoreKey},
								},
							},
						},
					},
				},
			},
		},
	})

	service := &unstructured.Unstructured{}
	service.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      remoteSignerName,
			"namespace": agent.Namespace,
			"labels":    asAnyMap(labels),
		},
		"spec": map[string]any{
			"type": "ClusterIP",
			"selector": map[string]any{
				"app.kubernetes.io/name":     remoteSignerName,
				"app.kubernetes.io/instance": agent.Name,
			},
			"ports": []any{
				map[string]any{
					"name":       "http",
					"port":       int64(remoteSignerPort),
					"targetPort": "http",
					"protocol":   "TCP",
				},
			},
		},
	})

	return []*unstructured.Unstructured{deployment, service}
}
