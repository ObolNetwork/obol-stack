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
// (chart 0.3.2 → image v0.3.0, the canonical recovery-id behaviour).
const (
	remoteSignerName        = "remote-signer"
	remoteSignerPort        = 9000
	remoteSignerImage       = "ghcr.io/obolnetwork/remote-signer:v0.3.0"
	remoteSignerKeystoreDir = "/keystores"
	remoteSignerSecretName  = "remote-signer-keystore"
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

	address, err := c.ensureSignerKeystore(ctx, agent.Namespace)
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
func (c *Controller) ensureSignerKeystore(ctx context.Context, namespace string) (string, error) {
	existing, err := c.client.Resource(monetizeapi.SecretGVR).Namespace(namespace).Get(ctx, remoteSignerSecretName, metav1.GetOptions{})
	if err == nil {
		annotations := existing.GetAnnotations()
		if addr := annotations[signerKeystoreAddressAnnotation]; addr != "" {
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

	secret := buildSignerKeystoreSecret(namespace, mat)
	if err := c.applyAgentObject(ctx, c.client.Resource(monetizeapi.SecretGVR).Namespace(namespace), secret); err != nil {
		return "", err
	}
	return mat.Address, nil
}

func buildSignerKeystoreSecret(namespace string, mat *openclaw.KeystoreMaterial) *unstructured.Unstructured {
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
			// V3 keystore filename matches its UUID by convention so
			// remote-signer's directory walker resolves it consistently
			// with the chart's flow.
			mat.KeystoreUUID + ".json": base64.StdEncoding.EncodeToString(mat.KeystoreJSON),
			"password":                 base64.StdEncoding.EncodeToString([]byte(mat.Password)),
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
							"env": []any{
								map[string]any{
									"name": "KEYSTORE_PASSWORD",
									"valueFrom": map[string]any{
										"secretKeyRef": map[string]any{
											"name": remoteSignerSecretName,
											"key":  "password",
										},
									},
								},
								map[string]any{"name": "KEYSTORE_PATH", "value": remoteSignerKeystoreDir},
							},
							"readinessProbe": map[string]any{
								"httpGet":             map[string]any{"path": "/health", "port": int64(remoteSignerPort)},
								"initialDelaySeconds": int64(2),
								"periodSeconds":       int64(5),
							},
							"livenessProbe": map[string]any{
								"httpGet":             map[string]any{"path": "/health", "port": int64(remoteSignerPort)},
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
								// Mount only the keystore JSON (and not
								// the password) into the directory — the
								// password is wired through env, not file.
								"items": []any{
									map[string]any{"key": "password", "path": ".password.skip"},
								},
								// Default-mode override irrelevant: fsGroup
								// + readOnly on volumeMount cover access.
							},
						},
					},
				},
			},
		},
	})
	// Patch the volume to also project the keystore JSON. The fixed
	// `items` list above only references `password`; we want the keystore
	// JSON file too. Using a projected items list lets us pick filenames
	// without parsing Secret keys at apply time.
	patchSignerVolumeWithKeystoreJSON(deployment)

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

// patchSignerVolumeWithKeystoreJSON drops the no-op .password.skip stub
// produced inline above and projects every Secret key as a file under
// /keystores. Done after the Deployment is fully constructed so the
// inline literal stays readable; a build-time-only post-step rather than
// branching the construction logic is the smaller change.
func patchSignerVolumeWithKeystoreJSON(deployment *unstructured.Unstructured) {
	volumes, _, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "volumes")
	if err != nil {
		return
	}
	for i, raw := range volumes {
		v, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if v["name"] != "keystore" {
			continue
		}
		secretMap, _ := v["secret"].(map[string]any)
		if secretMap == nil {
			continue
		}
		// Drop items so the Secret volume mounts every key as a file at
		// /keystores/<key>. The remote-signer's keystore-dir scan picks
		// up the .json file; "password" is a non-JSON sibling that the
		// scanner ignores.
		delete(secretMap, "items")
		v["secret"] = secretMap
		volumes[i] = v
	}
	_ = unstructured.SetNestedSlice(deployment.Object, volumes, "spec", "template", "spec", "volumes")
}
