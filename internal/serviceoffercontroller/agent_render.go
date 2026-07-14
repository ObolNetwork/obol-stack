package serviceoffercontroller

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Constants used across the rendered Agent manifests. Match the master
// agent's container UID/GID + Hermes image so the same data PVC layout
// works for both code paths. The image tag is read from env so the
// controller can be steered at deploy time without a recompile, with a
// safe production default if unset.
const (
	hermesContainerUID = 1000
	hermesContainerGID = 1000
	hermesPort         = 8642
	hermesServiceName  = "hermes"
	hermesConfigMap    = "hermes-config"
	hermesAPISecret    = "hermes-api-server"
	hermesEnvSecret    = "hermes-env"
	hermesProfileSeed  = "hermes-profile-seed"
	hermesDataPVC      = "hermes-data"
	hermesAPIPath      = "/health"
	// renovate: datasource=docker depName=nousresearch/hermes-agent
	defaultHermesImage = "nousresearch/hermes-agent:v2026.7.7.2"
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

	configYAML := renderHermesConfig(agent, litellmKey)

	out := []*unstructured.Unstructured{
		buildAgentNamespace(agent.Namespace),
		buildAgentServiceAccount(agent),
		buildAgentDataPVC(agent),
		buildAgentConfigMap(agent, configYAML),
		buildAgentAPISecret(agent, apiKey),
		buildAgentDeployment(agent, configYAML),
		buildAgentService(agent),
		buildAgentNetworkPolicy(agent),
	}
	return out, nil
}

// renderHermesConfig produces the Hermes config.yaml as a YAML string.
// We assemble it as a plain string rather than yaml.Marshal-ing a map
// so the embedded indentation in the ConfigMap stays exactly as Hermes
// expects, matching the master agent's known-good shape from
// internal/hermes.generateConfig.
//
// Optional AgentSpec fields (ModelProvider, MaxTurns, DisabledToolsets,
// MCPServers) override the historical defaults when set. When all are
// unset, output is byte-identical to the pre-field template.
//
// Sub-agent constraints: every Agent CR is a sub-agent-for-sale (the
// master is deployed via `obol agent init`, not via ServiceOffer), so the
// terminal/agent caps below apply unconditionally. The Cloudflare free
// tunnel cuts off requests at 100s, so lifetime_seconds is bounded under
// that. terminal.timeout must stay <= lifetime_seconds so no single
// operation can outlive the session. max_turns and reasoning_effort cap
// chattiness, and disabled_toolsets drops Hermes tool families that aren't
// useful in a paid-service context (memory persistence, web search).
//
// approvals.mode: off is required for a paid sub-agent. It is served
// headless (`hermes gateway run`, OpenAI-compatible API only) with no chat
// platform wired, so the buyer paying over x402 has no interactive channel
// to answer a dangerous-command / execute_code approval prompt. Left on the
// default (manual), a benign tool call (e.g. execute_code making an HTTP
// request) enqueues a pending approval no one can answer and the paid,
// streaming request stalls until the tunnel drops — the buyer pays and gets
// nothing. "off" (== HERMES_YOLO_MODE) is deliberately scoped to these
// sandboxed sub-agents, NOT the master agent (whose operator is present to
// approve). It is not "unrestricted": Hermes' unconditional HARDLINE floor
// still blocks catastrophic host commands (rm -rf /, mkfs, dd to a raw
// device, shutdown/reboot, fork bomb, kill -1) even under off, and the
// pod is already boxed in — non-root UID 1000, ephemeral 5Gi PVC, and the
// agent-isolation NetworkPolicy (cluster-closed, cloud-IMDS blocked). Quote
// "off" so the YAML parser keeps it the string "off" and never folds it to
// the boolean false.
func renderHermesConfig(agent *monetizeapi.Agent, litellmKey string) string {
	model := agent.EffectiveModel()
	var b strings.Builder

	// Model block: empty/"custom" => cluster LiteLLM path (historical default).
	// Any other provider omits base_url/api_key (credentials resolve in-pod).
	provider := agent.Spec.ModelProvider
	if provider == "" || provider == "custom" {
		fmt.Fprintf(&b, `model:
  default: %q
  provider: custom
  base_url: http://litellm.llm.svc.cluster.local:4000/v1
  api_key: %q
`, model, litellmKey)
	} else {
		fmt.Fprintf(&b, `model:
  default: %q
  provider: %s
`, model, provider)
	}

	maxTurns := 30
	if agent.Spec.MaxTurns != nil {
		maxTurns = *agent.Spec.MaxTurns
	}

	disabled := agent.Spec.DisabledToolsets
	if disabled == nil {
		disabled = []string{"memory", "web"}
	}

	fmt.Fprintf(&b, `terminal:
  backend: local
  cwd: /data/.hermes/workspace
  timeout: 80
  lifetime_seconds: 90
  docker_mount_cwd_to_workspace: false
agent:
  max_turns: %d
  reasoning_effort: low
  disabled_toolsets:
`, maxTurns)
	for _, ts := range disabled {
		fmt.Fprintf(&b, "    - %s\n", ts)
	}
	b.WriteString(`approvals:
  mode: "off"
skills:
  external_dirs:
    - /data/.hermes/obol-skills
`)

	// gateway block only when the operator set maxConcurrentRuns; nil =>
	// omit entirely so existing agents stay byte-identical.
	if agent.Spec.MaxConcurrentRuns != nil {
		fmt.Fprintf(&b, `gateway:
  api_server:
    max_concurrent_runs: %d
`, *agent.Spec.MaxConcurrentRuns)
	}

	// mcp_servers only when the operator listed servers; empty => omit entirely
	// so existing agents stay byte-identical to the pre-field template.
	if len(agent.Spec.MCPServers) > 0 {
		b.WriteString("mcp_servers:\n")
		for _, srv := range agent.Spec.MCPServers {
			fmt.Fprintf(&b, "  %s:\n", srv.Name)
			fmt.Fprintf(&b, "    command: %s\n", srv.Command)
			if len(srv.Args) > 0 {
				b.WriteString("    args:\n")
				for _, arg := range srv.Args {
					fmt.Fprintf(&b, "      - %s\n", arg)
				}
			}
			if srv.Timeout != nil {
				fmt.Fprintf(&b, "    timeout: %d\n", *srv.Timeout)
			}
			if len(srv.Env) > 0 {
				b.WriteString("    env:\n")
				keys := make([]string, 0, len(srv.Env))
				for k := range srv.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					// Emit env values verbatim — do not expand ${VAR}; Hermes
					// resolves interpolation in-pod at runtime.
					fmt.Fprintf(&b, "      %s: %s\n", k, srv.Env[k])
				}
			}
		}
	}

	return b.String()
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
	// Stamp the same sha256 hex used for Deployment's checksum/hermes-config
	// annotation so provisionAgent can skip rewrites when desired is unchanged.
	configHash := fmt.Sprintf("%x", sha256.Sum256([]byte(configYAML)))
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      hermesConfigMap,
			"namespace": agent.Namespace,
			"labels":    asAnyMap(agentLabels(agent.Name)),
			"annotations": map[string]any{
				hermesConfigHashAnnotation: configHash,
			},
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

func buildAgentDeployment(agent *monetizeapi.Agent, configYAML string) *unstructured.Unstructured {
	labels := agentLabels(agent.Name)
	configChecksum := sha256.Sum256([]byte(configYAML))
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
			"strategy": map[string]any{
				"type": "Recreate",
			},
			"selector": map[string]any{
				"matchLabels": map[string]any{
					"app.kubernetes.io/name":     labels["app.kubernetes.io/name"],
					"app.kubernetes.io/instance": agent.Name,
				},
			},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": asAnyMap(labels),
					"annotations": map[string]any{
						"checksum/hermes-config": fmt.Sprintf("%x", configChecksum),
					},
				},
				"spec": agentPodSpec(agent),
			},
		},
	})
	return u
}

func buildAgentConfigInitContainer() map[string]any {
	return map[string]any{
		"name":            "config-seed",
		"image":           hermesImage(),
		"imagePullPolicy": "IfNotPresent",
		"command":         []any{"/bin/sh", "-ceu"},
		"args": []any{`mkdir -p /data/.hermes
cp /config-seed/config.yaml /data/.hermes/config.yaml
chmod 600 /data/.hermes/config.yaml
`},
		"volumeMounts": []any{
			map[string]any{"name": "data", "mountPath": "/data"},
			map[string]any{"name": "config", "mountPath": "/config-seed"},
		},
	}
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
		// v2026.7.x images bake HERMES_WRITE_SAFE_ROOT=/opt/data (their default
		// HERMES_HOME); with HERMES_HOME relocated to the PVC the safe root must
		// follow or every file-tool write is denied.
		map[string]any{"name": "HERMES_WRITE_SAFE_ROOT", "value": "/data/.hermes:/tmp"},
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
		// Bearer token for the signer's REST API; skill scripts
		// (ethereum-local-wallet signer.py and everything importing it)
		// attach it as an Authorization header when set. optional: the
		// Secret key is backfilled by the controller on pre-auth
		// keystores, and an absent token simply means the (older) signer
		// runs auth-disabled.
		containerEnv = append(containerEnv, map[string]any{
			"name": "REMOTE_SIGNER_TOKEN",
			"valueFrom": map[string]any{
				"secretKeyRef": map[string]any{
					"name":     remoteSignerSecretName,
					"key":      remoteSignerAuthTokenKey,
					"optional": true,
				},
			},
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
			"runAsNonRoot":        true,
			"runAsUser":           int64(hermesContainerUID),
			"runAsGroup":          int64(hermesContainerGID),
			"fsGroup":             int64(hermesContainerGID),
			"fsGroupChangePolicy": "Always",
		},
		"initContainers": []any{
			buildAgentConfigInitContainer(),
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

// Cluster CIDRs blocked by the agent NetworkPolicy's internet-egress rule.
// These are k3s defaults and the stack's k3d/k3s configs do not override
// them. Same stance as hermesImage(): pinned constants over host env
// plumbing; operators running a non-default CIDR layout can patch the
// rendered policy (or we grow controller env overrides when that becomes
// real).
const (
	clusterPodCIDR     = "10.42.0.0/16"
	clusterServiceCIDR = "10.43.0.0/16"
	// Link-local (RFC 3927), which includes the cloud instance-metadata
	// endpoint 169.254.169.254 (IMDS). Agents run semi-untrusted skill
	// code that fetches arbitrary URLs; on a cloud node an SSRF to IMDS
	// could exfiltrate the node's instance credentials. It is never a
	// legitimate egress target for an agent, and excluding it does NOT
	// affect apiserver reachability — kube-proxy DNATs kubernetes.default
	// to the node's real (non-link-local) address. RFC1918 host/LAN ranges
	// are deliberately left reachable: the local-first stack expects agents
	// to reach host services (e.g. host.k3d.internal).
	linkLocalCIDR = "169.254.0.0/16"
)

// buildAgentNetworkPolicy locks an agent business namespace down to the
// "internet-open, cluster-closed" shape from
// plans/agent-business-architecture.md §3.5:
//
//   - Ingress: paid traffic only (Traefik ForwardAuth path and the
//     x402-verifier HandleProxy path, both restricted to the Hermes port),
//     same-namespace traffic (hermes ↔ remote-signer), and Prometheus
//     scrapes from the monitoring namespace. Crucially, NOTHING else
//     reaches the remote-signer (port 9000): agent A can no longer call
//     agent B's signer or Hermes API.
//   - Egress: kube-dns, LiteLLM (llm:4000), eRPC (erpc:80/4001), Traefik
//     (80/443 — in-pod paid requests to other sellers go via
//     traefik.traefik.svc), same-namespace, and the public internet via an
//     ipBlock that excludes the cluster pod/service CIDRs. The apiserver
//     stays reachable: kube-proxy DNATs kubernetes.default to the host
//     process address, which lands OUTSIDE the cluster CIDRs and is
//     therefore allowed by the internet rule — this is what makes the
//     policy portable on k3d/Flannel where a positive apiserver allowlist
//     is not (see the frontend-egress revert note in helmfile.yaml).
//
// Selected namespaces are matched on the immutable
// kubernetes.io/metadata.name label (set by the apiserver since k8s 1.22).
// k3s ships its embedded NetworkPolicy controller enabled, so this
// enforces on the default stack.
func buildAgentNetworkPolicy(agent *monetizeapi.Agent) *unstructured.Unstructured {
	nsMatch := func(name string) map[string]any {
		return map[string]any{
			"namespaceSelector": map[string]any{
				"matchLabels": map[string]any{"kubernetes.io/metadata.name": name},
			},
		}
	}
	tcpPort := func(port int64) map[string]any {
		return map[string]any{"port": port, "protocol": "TCP"}
	}

	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      "agent-isolation",
			"namespace": agent.Namespace,
			"labels":    asAnyMap(agentLabels(agent.Name)),
		},
		"spec": map[string]any{
			"podSelector": map[string]any{},
			"policyTypes": []any{"Ingress", "Egress"},
			"ingress": []any{
				// Same-namespace: hermes <-> remote-signer.
				map[string]any{"from": []any{map[string]any{"podSelector": map[string]any{}}}},
				// Paid traffic to the Hermes API only — never the signer.
				map[string]any{
					"from":  []any{nsMatch("traefik"), nsMatch("x402")},
					"ports": []any{tcpPort(hermesPort)},
				},
				// Prometheus scrapes (any port, future-proof for hermes/
				// signer metrics exporters).
				map[string]any{"from": []any{nsMatch("monitoring")}},
			},
			"egress": []any{
				// Same-namespace: hermes -> its own remote-signer :9000.
				map[string]any{"to": []any{map[string]any{"podSelector": map[string]any{}}}},
				// DNS.
				map[string]any{
					"to": []any{map[string]any{
						"namespaceSelector": map[string]any{
							"matchLabels": map[string]any{"kubernetes.io/metadata.name": "kube-system"},
						},
						"podSelector": map[string]any{
							"matchLabels": map[string]any{"k8s-app": "kube-dns"},
						},
					}},
					"ports": []any{
						map[string]any{"port": int64(53), "protocol": "UDP"},
						map[string]any{"port": int64(53), "protocol": "TCP"},
					},
				},
				// Inference via LiteLLM.
				map[string]any{"to": []any{nsMatch("llm")}, "ports": []any{tcpPort(4000)}},
				// Chain reads via eRPC (80 = JSON-RPC, 4001 = metrics/aux).
				map[string]any{"to": []any{nsMatch("erpc")}, "ports": []any{tcpPort(80), tcpPort(4001)}},
				// Buying from other sellers goes through Traefik's
				// cluster-internal address (obol.stack doesn't resolve
				// in-pod). No port constraint: NetworkPolicy ports match
				// the post-DNAT POD port, and Traefik's are named
				// targetPorts (web=8000, websecure=8443) that numeric
				// rules can't address portably — the namespace boundary
				// is the control here, and the traefik namespace is the
				// public ingress plane anyway.
				map[string]any{"to": []any{nsMatch("traefik")}},
				// Public internet (skills fetching URLs, facilitators,
				// RPCs) and — via post-DNAT host address — the apiserver.
				// Cluster pod/service CIDRs are excluded so this never
				// reopens cross-namespace traffic; link-local is excluded
				// so semi-untrusted skill code cannot SSRF the cloud IMDS
				// endpoint (169.254.169.254).
				map[string]any{
					"to": []any{map[string]any{
						"ipBlock": map[string]any{
							"cidr":   "0.0.0.0/0",
							"except": []any{clusterPodCIDR, clusterServiceCIDR, linkLocalCIDR},
						},
					}},
				},
			},
		},
	})
	return u
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
