package serviceoffercontroller

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Constants used across the rendered Agent manifests. Match the master
// agent's container UID/GID + Hermes image so the same data PVC layout
// works for both code paths. The image tag is read from env so the
// controller can be steered at deploy time without a recompile, with a
// safe production default if unset.
const (
	hermesContainerUID = 10000
	hermesContainerGID = 10000
	hermesPort         = 8642
	hermesServiceName  = "hermes"
	hermesConfigMap    = "hermes-config"
	hermesAPISecret    = "hermes-api-server"
	hermesEnvSecret    = "hermes-env"
	hermesProfileSeed  = "hermes-profile-seed"
	hermesDataPVC      = "hermes-data"
	hermesAPIPath      = "/health"
	defaultHermesImage = "nousresearch/hermes-agent:v2026.5.28"
)

// agentLabels returns the standard label set we attach to every primitive
// rendered for an Agent. apiservers join on app.kubernetes.io/name to
// match Pods to Services, and obol.org/agent gives tooling a single key
// to filter all sub-agents.
func agentLabels(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       hermesServiceName,
		"app.kubernetes.io/managed-by": "serviceoffer-controller",
		"app.kubernetes.io/instance":   name,
		"obol.org/agent":               name,
	}
}

// agentManifests returns the K8s primitives the controller must apply to
// realise an Agent CR. Caller wires litellmKey (the cluster master key)
// into the Hermes config so the agent can route inference; apiKey is the
// per-agent API server bearer token. Both are passed in by the caller so
// the renderer stays testable without secret-reading side effects.
//
// The data PVC is intentionally a plain ReadWriteOnce claim with no
// storageClass override — local-path-provisioner (configured by
// local-path.yaml) maps it to <DataDir>/<namespace>/<pvc-name>/, the
// same path agentcrd.HostHomePath writes to. So the host-side seed of
// SOUL.md + skills lands inside the pod automatically. A small init container
// also supports a future factory-created profile archive Secret, so profile
// templates can be imported once without making the Agent CRD schema carry
// profile bytes. This is the single non-obvious
// invariant; if you change the namespace prefix on either side the
// volume contents won't line up.
func agentManifests(agent *monetizeapi.Agent, litellmKey, apiKey string) ([]*unstructured.Unstructured, error) {
	if agent == nil {
		return nil, fmt.Errorf("agentManifests: nil agent")
	}
	if agent.Namespace == "" || agent.Name == "" {
		return nil, fmt.Errorf("agentManifests: agent missing namespace/name")
	}
	model := agent.EffectiveModel()
	if model == "" {
		return nil, fmt.Errorf("agentManifests: agent has no resolved model")
	}

	configYAML := renderHermesConfig(model, litellmKey)

	out := []*unstructured.Unstructured{
		buildAgentNamespace(agent.Namespace),
		buildAgentServiceAccount(agent),
		buildAgentDataPVC(agent),
		buildAgentConfigMap(agent, configYAML),
		buildAgentAPISecret(agent, apiKey),
		buildAgentDeployment(agent),
		buildAgentService(agent),
	}
	return out, nil
}

// renderHermesConfig produces the Hermes config.yaml as a YAML string.
// We assemble it as a plain string rather than yaml.Marshal-ing a map
// so the embedded indentation in the ConfigMap stays exactly as Hermes
// expects, matching the master agent's known-good shape from
// internal/hermes.generateConfig.
func renderHermesConfig(model, litellmKey string) string {
	return fmt.Sprintf(`model:
  default: %q
  provider: custom
  base_url: http://litellm.llm.svc.cluster.local:4000/v1
  api_key: %q
terminal:
  backend: local
  cwd: /data/.hermes/workspace
  timeout: 180
  lifetime_seconds: 300
  docker_mount_cwd_to_workspace: false
skills:
  external_dirs:
    - /data/.hermes/obol-skills
`, model, litellmKey)
}

func buildAgentNamespace(ns string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": ns,
			"labels": map[string]any{
				"obol.org/agent-namespace":     "true",
				"app.kubernetes.io/managed-by": "serviceoffer-controller",
			},
		},
	})
	return u
}

func buildAgentServiceAccount(agent *monetizeapi.Agent) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata": map[string]any{
			"name":      hermesServiceName,
			"namespace": agent.Namespace,
			"labels":    asAnyMap(agentLabels(agent.Name)),
		},
		"automountServiceAccountToken": true,
	})
	return u
}

func buildAgentDataPVC(agent *monetizeapi.Agent) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata": map[string]any{
			"name":      hermesDataPVC,
			"namespace": agent.Namespace,
			"labels":    asAnyMap(agentLabels(agent.Name)),
		},
		"spec": map[string]any{
			"accessModes": []any{"ReadWriteOnce"},
			"resources": map[string]any{
				"requests": map[string]any{"storage": "5Gi"},
			},
		},
	})
	return u
}

func buildAgentConfigMap(agent *monetizeapi.Agent, configYAML string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      hermesConfigMap,
			"namespace": agent.Namespace,
			"labels":    asAnyMap(agentLabels(agent.Name)),
		},
		"data": map[string]any{"config.yaml": configYAML},
	})
	return u
}

func buildAgentAPISecret(agent *monetizeapi.Agent, apiKey string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      hermesAPISecret,
			"namespace": agent.Namespace,
			"labels":    asAnyMap(agentLabels(agent.Name)),
		},
		"type":       "Opaque",
		"stringData": map[string]any{"API_SERVER_KEY": apiKey},
	})
	return u
}

func buildAgentDeployment(agent *monetizeapi.Agent) *unstructured.Unstructured {
	labels := agentLabels(agent.Name)
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      hermesServiceName,
			"namespace": agent.Namespace,
			"labels":    asAnyMap(labels),
		},
		"spec": map[string]any{
			"replicas": int64(1),
			"selector": map[string]any{
				"matchLabels": map[string]any{
					"app.kubernetes.io/name":     labels["app.kubernetes.io/name"],
					"app.kubernetes.io/instance": agent.Name,
				},
			},
			"template": map[string]any{
				"metadata": map[string]any{"labels": asAnyMap(labels)},
				"spec":     agentPodSpec(agent),
			},
		},
	})
	return u
}

func buildAgentProfileInitContainer() map[string]any {
	return map[string]any{
		"name":            "profile-seed",
		"image":           hermesImage(),
		"imagePullPolicy": "IfNotPresent",
		"command":         []any{"/bin/sh", "-ceu"},
		"args": []any{`mkdir -p /data/.hermes/home /data/.hermes/workspace /data/.hermes/logs /data/.hermes/obol-skills

seed=/profile-seed/profile.tar.gz
marker=/data/.hermes/.obol-profile-seed-imported
if [ -f "$seed" ] && [ ! -f "$marker" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  if ! tar -tzf "$seed" | awk '(/^\/|(^|\/)\.\.(\/|$)/) { bad=1 } END { exit bad }'; then
    echo "profile seed archive contains an unsafe path" >&2
    exit 1
  fi
  tar -xzf "$seed" -C "$tmp"
  roots="$(find "$tmp" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
  if [ "$roots" != "1" ]; then
    echo "profile seed archive must contain exactly one top-level directory" >&2
    exit 1
  fi
  root="$(find "$tmp" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
  bad_member="$(find "$root" ! -type f ! -type d -print -quit)"
  if [ -n "$bad_member" ]; then
    echo "profile seed archive contains unsupported member: $bad_member" >&2
    exit 1
  fi
  cp -R "$root"/. /data/.hermes/
  touch "$marker"
fi

if [ -f /data/.hermes/soul.md ] && [ ! -f /data/.hermes/SOUL.md ]; then
  cp /data/.hermes/soul.md /data/.hermes/SOUL.md
fi
`},
		"volumeMounts": []any{
			map[string]any{"name": "data", "mountPath": "/data"},
			map[string]any{"name": "profile-seed", "mountPath": "/profile-seed", "readOnly": true},
		},
	}
}

func agentPodSpec(agent *monetizeapi.Agent) map[string]any {
	containerEnv := []any{
		map[string]any{"name": "HERMES_HOME", "value": "/data/.hermes"},
		map[string]any{"name": "HOME", "value": "/data/.hermes/home"},
		map[string]any{"name": "API_SERVER_ENABLED", "value": "true"},
		map[string]any{"name": "API_SERVER_HOST", "value": "0.0.0.0"},
		map[string]any{"name": "API_SERVER_PORT", "value": fmt.Sprint(hermesPort)},
		map[string]any{
			"name": "API_SERVER_KEY",
			"valueFrom": map[string]any{
				"secretKeyRef": map[string]any{"name": hermesAPISecret, "key": "API_SERVER_KEY"},
			},
		},
		map[string]any{"name": "API_SERVER_MODEL_NAME", "value": agent.EffectiveModel()},
		map[string]any{"name": "AGENT_NAMESPACE", "value": agent.Namespace},
		map[string]any{"name": "OBOL_SKILLS_DIR", "value": "/data/.hermes/obol-skills"},
		// CRD agents expose only the API (gated by API_SERVER_KEY and reached
		// only through the in-cluster Traefik route + x402 ForwardAuth). No
		// Telegram/Discord/dashboard platforms are wired, so Hermes' user
		// gateway has nothing to actually gate — "allow all" silences its
		// startup warning without opening any real surface. If platform
		// integrations are ever added, swap this for explicit per-platform
		// allowlists.
		map[string]any{"name": "GATEWAY_ALLOW_ALL_USERS", "value": "true"},
	}
	if agent.Status.WalletAddress != "" {
		containerEnv = append(containerEnv, map[string]any{
			"name": "AGENT_WALLET_ADDRESS", "value": agent.Status.WalletAddress,
		})
	}
	if agent.Spec.Wallet.Create {
		// Wired even when status.WalletAddress is still empty: skill code
		// already running inside Hermes can probe remote-signer for keys
		// during the brief provisioning window between Wallet ensure and
		// the next status patch.
		containerEnv = append(containerEnv, map[string]any{
			"name":  "REMOTE_SIGNER_URL",
			"value": fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", remoteSignerName, agent.Namespace, remoteSignerPort),
		})
	}

	probe := map[string]any{
		"httpGet":             map[string]any{"path": hermesAPIPath, "port": int64(hermesPort)},
		"initialDelaySeconds": int64(5),
		"periodSeconds":       int64(10),
	}
	startup := map[string]any{
		"httpGet":          map[string]any{"path": hermesAPIPath, "port": int64(hermesPort)},
		"periodSeconds":    int64(5),
		"failureThreshold": int64(24),
	}

	return map[string]any{
		"serviceAccountName":           hermesServiceName,
		"automountServiceAccountToken": true,
		"securityContext": map[string]any{
			"runAsUser":           int64(hermesContainerUID),
			"runAsGroup":          int64(hermesContainerGID),
			"fsGroup":             int64(hermesContainerGID),
			"fsGroupChangePolicy": "OnRootMismatch",
		},
		"initContainers": []any{
			buildAgentProfileInitContainer(),
		},
		"containers": []any{
			map[string]any{
				"name":            hermesServiceName,
				"image":           hermesImage(),
				"imagePullPolicy": "IfNotPresent",
				"command":         []any{"/opt/hermes/.venv/bin/hermes"},
				"args":            []any{"gateway", "run", "--replace"},
				"ports": []any{
					map[string]any{"name": "http", "containerPort": int64(hermesPort)},
				},
				"env": containerEnv,
				"envFrom": []any{
					map[string]any{
						"secretRef": map[string]any{
							"name":     hermesEnvSecret,
							"optional": true,
						},
					},
				},
				"readinessProbe": probe,
				"livenessProbe":  probe,
				"startupProbe":   startup,
				"volumeMounts": []any{
					map[string]any{"name": "data", "mountPath": "/data"},
					map[string]any{
						"name":      "config",
						"mountPath": "/data/.hermes/config.yaml",
						"subPath":   "config.yaml",
						"readOnly":  true,
					},
				},
			},
		},
		"volumes": []any{
			map[string]any{
				"name": "data",
				"persistentVolumeClaim": map[string]any{
					"claimName": hermesDataPVC,
				},
			},
			map[string]any{
				"name": "profile-seed",
				"secret": map[string]any{
					"secretName": hermesProfileSeed,
					"optional":   true,
					"items": []any{
						map[string]any{"key": "profile.tar.gz", "path": "profile.tar.gz"},
					},
				},
			},
			map[string]any{
				"name": "config",
				"configMap": map[string]any{
					"name": hermesConfigMap,
					"items": []any{
						map[string]any{"key": "config.yaml", "path": "config.yaml"},
					},
				},
			},
		},
	}
}

func buildAgentService(agent *monetizeapi.Agent) *unstructured.Unstructured {
	labels := agentLabels(agent.Name)
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      hermesServiceName,
			"namespace": agent.Namespace,
			"labels":    asAnyMap(labels),
		},
		"spec": map[string]any{
			"type": "ClusterIP",
			"selector": map[string]any{
				"app.kubernetes.io/name":     labels["app.kubernetes.io/name"],
				"app.kubernetes.io/instance": agent.Name,
			},
			"ports": []any{
				map[string]any{
					"name":       "http",
					"port":       int64(hermesPort),
					"targetPort": "http",
					"protocol":   "TCP",
				},
			},
		},
	})
	return u
}

func hermesImage() string {
	// The cluster-side controller intentionally does not consult host
	// env vars; it would require leaking OBOL_HERMES_IMAGE into the pod
	// spec, which is brittle. Operators wanting to override the image
	// can patch the controller Deployment's HERMES_IMAGE env directly,
	// at which point this function reads it via os.Getenv. For now we
	// pin the same default as internal/hermes/hermes.go to keep
	// behaviour identical across master and sub-agents.
	return defaultHermesImage
}

func asAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// generateAPIKey returns a fresh 32-byte hex API server token. Used only
// when no per-agent Secret yet exists; existing tokens are preserved
// across reconciles to keep API clients stable.
func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate API key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
