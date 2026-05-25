package serviceoffercontroller

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

const (
	skillCatalogNamespace     = "x402"
	skillCatalogConfigMapName = "obol-skill-md"
	skillCatalogRouteName     = "obol-skill-md-route"
	servicesJSONRouteName     = "obol-services-json-route"
)

// restrictedPodSecurityContext returns a Pod-level securityContext that
// satisfies the Restricted Pod Security Standard (PSS). PR #521 enforces
// Restricted PSS on the x402 namespace, so the controller-rendered httpd
// workloads (obol-skill-md and agentidentity-*-registration) must ship a
// compliant securityContext or they fail admission and never start.
//
// UID/GID 1000 is the canonical non-root user available in the busybox
// image used by both Deployments. fsGroup keeps the projected ConfigMap
// volumes readable by the httpd process.
func restrictedPodSecurityContext() map[string]any {
	return map[string]any{
		"runAsNonRoot": true,
		"runAsUser":    int64(1000),
		"runAsGroup":   int64(1000),
		"fsGroup":      int64(1000),
		"seccompProfile": map[string]any{
			"type": "RuntimeDefault",
		},
	}
}

// restrictedContainerSecurityContext returns a container-level
// securityContext compliant with the Restricted PSS profile: privilege
// escalation disabled and all Linux capabilities dropped.
func restrictedContainerSecurityContext() map[string]any {
	return map[string]any{
		"allowPrivilegeEscalation": false,
		"capabilities": map[string]any{
			"drop": []any{"ALL"},
		},
	}
}

func buildRegistrationRequest(offer *monetizeapi.ServiceOffer, desiredState string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": monetizeapi.Group + "/" + monetizeapi.Version,
			"kind":       monetizeapi.RegistrationRequestKind,
			"metadata": map[string]any{
				"name":            registrationRequestName(offer.Name),
				"namespace":       offer.Namespace,
				"ownerReferences": []any{ownerRefMap(offer)},
			},
			"spec": map[string]any{
				"serviceOfferName":      offer.Name,
				"serviceOfferNamespace": offer.Namespace,
				"desiredState":          desiredState,
				"chain":                 offer.Spec.Payment.Network,
			},
		},
	}
}

func buildAgentIdentityRegistrationConfigMap(identity *monetizeapi.AgentIdentity, documentJSON string) *unstructured.Unstructured {
	name := agentIdentityRegistrationName(identity)
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       identity.Namespace,
				"ownerReferences": []any{agentIdentityOwnerRefMap(identity)},
				"labels":          agentIdentityLabels(identity, name),
			},
			"data": map[string]any{
				"agent-registration.json": documentJSON,
				"httpd.conf":              ".json:application/json\n",
			},
		},
	}
}

func buildAgentIdentityRegistrationDeployment(identity *monetizeapi.AgentIdentity, contentHash string) *unstructured.Unstructured {
	name := agentIdentityRegistrationName(identity)
	labels := agentIdentityLabels(identity, name)
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       identity.Namespace,
				"ownerReferences": []any{agentIdentityOwnerRefMap(identity)},
				"labels":          labels,
			},
			"spec": map[string]any{
				"replicas": int64(1),
				"selector": map[string]any{
					"matchLabels": labels,
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"labels": labels,
						"annotations": map[string]any{
							"obol.org/content-hash": contentHash,
						},
					},
					"spec": map[string]any{
						"securityContext": restrictedPodSecurityContext(),
						"containers": []any{
							map[string]any{
								"name":            "httpd",
								"image":           "busybox:1.36",
								"command":         []any{"httpd", "-f", "-p", "8080", "-h", "/www"},
								"securityContext": restrictedContainerSecurityContext(),
								"ports": []any{
									map[string]any{"containerPort": int64(8080), "protocol": "TCP"},
								},
								"volumeMounts": []any{
									map[string]any{"name": "content", "mountPath": "/www", "readOnly": true},
									map[string]any{"name": "httpdconf", "mountPath": "/etc/httpd.conf", "subPath": "httpd.conf", "readOnly": true},
								},
								"resources": map[string]any{
									"requests": map[string]any{"cpu": "5m", "memory": "8Mi"},
									"limits":   map[string]any{"cpu": "50m", "memory": "32Mi"},
								},
							},
						},
						"volumes": []any{
							map[string]any{
								"name": "content",
								"configMap": map[string]any{
									"name":  name,
									"items": []any{map[string]any{"key": "agent-registration.json", "path": ".well-known/agent-registration.json"}},
								},
							},
							map[string]any{
								"name": "httpdconf",
								"configMap": map[string]any{
									"name":  name,
									"items": []any{map[string]any{"key": "httpd.conf", "path": "httpd.conf"}},
								},
							},
						},
					},
				},
			},
		},
	}
}

func buildAgentIdentityRegistrationService(identity *monetizeapi.AgentIdentity) *unstructured.Unstructured {
	name := agentIdentityRegistrationName(identity)
	labels := agentIdentityLabels(identity, name)
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       identity.Namespace,
				"ownerReferences": []any{agentIdentityOwnerRefMap(identity)},
				"labels":          labels,
			},
			"spec": map[string]any{
				"type":     "ClusterIP",
				"selector": labels,
				"ports": []any{
					map[string]any{"port": int64(8080), "targetPort": int64(8080), "protocol": "TCP"},
				},
			},
		},
	}
}

func buildAgentIdentityRegistrationHTTPRoute(identity *monetizeapi.AgentIdentity) *unstructured.Unstructured {
	name := agentIdentityRouteName(identity)
	serviceName := agentIdentityRegistrationName(identity)
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       identity.Namespace,
				"ownerReferences": []any{agentIdentityOwnerRefMap(identity)},
				"labels":          agentIdentityLabels(identity, serviceName),
			},
			"spec": map[string]any{
				"parentRefs": []any{
					map[string]any{
						"name":        "traefik-gateway",
						"namespace":   "traefik",
						"sectionName": "web",
					},
				},
				"rules": []any{
					map[string]any{
						"matches": []any{
							map[string]any{
								"path": map[string]any{
									"type":  "Exact",
									"value": "/.well-known/agent-registration.json",
								},
							},
						},
						"backendRefs": []any{
							map[string]any{
								"name":      serviceName,
								"namespace": identity.Namespace,
								"port":      int64(8080),
							},
						},
					},
				},
			},
		},
	}
}

func agentIdentityLabels(identity *monetizeapi.AgentIdentity, appName string) map[string]any {
	return map[string]any{
		"app":                    appName,
		"obol.org/agentidentity": identity.Name,
		"obol.org/managed-by":    "serviceoffer-controller",
	}
}

func buildSkillCatalogConfigMap(content, servicesJSON string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      skillCatalogConfigMapName,
				"namespace": skillCatalogNamespace,
				"labels": map[string]any{
					"app":                 skillCatalogConfigMapName,
					"obol.org/managed-by": "serviceoffer-controller",
				},
			},
			"data": map[string]any{
				"skill.md":      content,
				"services.json": servicesJSON,
				"httpd.conf":    ".md:text/markdown\n.json:application/json\n",
			},
		},
	}
}

func buildSkillCatalogDeployment(contentHash string) *unstructured.Unstructured {
	labels := map[string]any{
		"app":                 skillCatalogConfigMapName,
		"obol.org/managed-by": "serviceoffer-controller",
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":      skillCatalogConfigMapName,
				"namespace": skillCatalogNamespace,
				"labels":    labels,
			},
			"spec": map[string]any{
				"replicas": int64(1),
				"selector": map[string]any{
					"matchLabels": labels,
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"labels": labels,
						"annotations": map[string]any{
							"obol.org/content-hash": contentHash,
						},
					},
					"spec": map[string]any{
						"securityContext": restrictedPodSecurityContext(),
						"containers": []any{
							map[string]any{
								"name":            "httpd",
								"image":           "busybox:1.36",
								"command":         []any{"httpd", "-f", "-p", "8080", "-h", "/www"},
								"securityContext": restrictedContainerSecurityContext(),
								"ports": []any{
									map[string]any{"containerPort": int64(8080), "protocol": "TCP"},
								},
								"volumeMounts": []any{
									map[string]any{"name": "content", "mountPath": "/www", "readOnly": true},
									map[string]any{"name": "httpdconf", "mountPath": "/etc/httpd.conf", "subPath": "httpd.conf", "readOnly": true},
								},
								"resources": map[string]any{
									"requests": map[string]any{"cpu": "5m", "memory": "8Mi"},
									"limits":   map[string]any{"cpu": "50m", "memory": "32Mi"},
								},
							},
						},
						"volumes": []any{
							map[string]any{
								"name": "content",
								"configMap": map[string]any{
									"name": skillCatalogConfigMapName,
									"items": []any{
										map[string]any{"key": "skill.md", "path": "skill.md"},
										map[string]any{"key": "services.json", "path": "api/services.json"},
									},
								},
							},
							map[string]any{
								"name": "httpdconf",
								"configMap": map[string]any{
									"name":  skillCatalogConfigMapName,
									"items": []any{map[string]any{"key": "httpd.conf", "path": "httpd.conf"}},
								},
							},
						},
					},
				},
			},
		},
	}
}

func buildSkillCatalogService() *unstructured.Unstructured {
	labels := map[string]any{
		"app":                 skillCatalogConfigMapName,
		"obol.org/managed-by": "serviceoffer-controller",
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":      skillCatalogConfigMapName,
				"namespace": skillCatalogNamespace,
				"labels":    labels,
			},
			"spec": map[string]any{
				"type":     "ClusterIP",
				"selector": labels,
				"ports": []any{
					map[string]any{"port": int64(8080), "targetPort": int64(8080), "protocol": "TCP"},
				},
			},
		},
	}
}

func buildSkillCatalogHTTPRoute() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]any{
				"name":      skillCatalogRouteName,
				"namespace": skillCatalogNamespace,
				"labels": map[string]any{
					"obol.org/managed-by": "serviceoffer-controller",
				},
			},
			"spec": map[string]any{
				"parentRefs": []any{
					map[string]any{
						"name":        "traefik-gateway",
						"namespace":   "traefik",
						"sectionName": "web",
					},
				},
				"rules": []any{
					map[string]any{
						"matches": []any{
							map[string]any{
								"path": map[string]any{
									"type":  "Exact",
									"value": "/skill.md",
								},
							},
						},
						"backendRefs": []any{
							map[string]any{
								"name":      skillCatalogConfigMapName,
								"namespace": skillCatalogNamespace,
								"port":      int64(8080),
							},
						},
					},
				},
			},
		},
	}
}

func buildServicesJSONHTTPRoute() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]any{
				"name":      servicesJSONRouteName,
				"namespace": skillCatalogNamespace,
				"labels": map[string]any{
					"obol.org/managed-by": "serviceoffer-controller",
				},
			},
			"spec": map[string]any{
				"parentRefs": []any{
					map[string]any{
						"name":        "traefik-gateway",
						"namespace":   "traefik",
						"sectionName": "web",
					},
				},
				"rules": []any{
					map[string]any{
						"matches": []any{
							map[string]any{
								"path": map[string]any{
									"type":  "Exact",
									"value": "/api/services.json",
								},
							},
						},
						"backendRefs": []any{
							map[string]any{
								"name":      skillCatalogConfigMapName,
								"namespace": skillCatalogNamespace,
								"port":      int64(8080),
							},
						},
					},
				},
			},
		},
	}
}

func buildHTTPRoute(offer *monetizeapi.ServiceOffer) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]any{
				"name":            childName(offer.Name),
				"namespace":       offer.Namespace,
				"ownerReferences": []any{ownerRefMap(offer)},
			},
			"spec": map[string]any{
				"parentRefs": []any{
					map[string]any{
						"name":        "traefik-gateway",
						"namespace":   "traefik",
						"sectionName": "web",
					},
				},
				"rules": []any{
					map[string]any{
						"matches": []any{
							map[string]any{
								"path": map[string]any{
									"type":  "PathPrefix",
									"value": offer.EffectivePath(),
								},
							},
						},
						"backendRefs": []any{
							map[string]any{
								"name":      "x402-verifier",
								"namespace": "x402",
								"port":      int64(8080),
							},
						},
					},
				},
			},
		},
	}
	return obj
}

func buildReferenceGrant(offer *monetizeapi.ServiceOffer) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1beta1",
			"kind":       "ReferenceGrant",
			"metadata": map[string]any{
				"name":      backendReferenceGrantName(offer.Name),
				"namespace": "x402",
				"labels": map[string]any{
					"obol.org/serviceoffer-namespace": offer.Namespace,
					"obol.org/serviceoffer-name":      offer.Name,
					"obol.org/managed-by":             "serviceoffer-controller",
				},
			},
			"spec": map[string]any{
				"from": []any{
					map[string]any{
						"group":     "gateway.networking.k8s.io",
						"kind":      "HTTPRoute",
						"namespace": offer.Namespace,
					},
				},
				"to": []any{
					map[string]any{
						"group": "",
						"kind":  "Service",
						"name":  "x402-verifier",
					},
				},
			},
		},
	}
}

// maxK8sNameLen is the maximum length for a Kubernetes resource name (DNS subdomain).
const maxK8sNameLen = 253

// safeName truncates a name to fit within Kubernetes DNS naming limits after
// applying the given prefix and suffix. If truncation is needed, a short hash
// is appended to avoid collisions.
func safeName(prefix, name, suffix string) string {
	full := prefix + name + suffix
	if len(full) <= maxK8sNameLen {
		return full
	}
	hash := fmt.Sprintf("%x", md5.Sum([]byte(name)))[:8]
	maxName := maxK8sNameLen - len(prefix) - len(suffix) - 1 - len(hash) // 1 for the dash before hash
	if maxName < 1 {
		maxName = 1
	}
	return prefix + name[:maxName] + "-" + hash + suffix
}

func childName(name string) string {
	return safeName("so-", name, "")
}

func backendReferenceGrantName(name string) string {
	return safeName("so-", name, "-backend-grant")
}

func registrationRequestName(name string) string {
	return safeName("so-", name, "-registration")
}

func registrationWorkloadName(requestName string) string {
	return requestName
}

func registrationRouteName(name string) string {
	return safeName("so-", name, "-wellknown")
}

func agentIdentityRegistrationName(identity *monetizeapi.AgentIdentity) string {
	if identity == nil || identity.Name == "" {
		return safeName("agentidentity-", monetizeapi.AgentIdentityDefaultName, "-registration")
	}
	return safeName("agentidentity-", identity.Name, "-registration")
}

func agentIdentityRouteName(identity *monetizeapi.AgentIdentity) string {
	if identity == nil || identity.Name == "" {
		return safeName("agentidentity-", monetizeapi.AgentIdentityDefaultName, "-wellknown")
	}
	return safeName("agentidentity-", identity.Name, "-wellknown")
}

func ownerRefMap(offer *monetizeapi.ServiceOffer) map[string]any {
	return ownerRefMapFor(monetizeapi.Group+"/"+monetizeapi.Version, monetizeapi.ServiceOfferKind, offer.Name, offer.UID)
}

func agentIdentityOwnerRefMap(identity *monetizeapi.AgentIdentity) map[string]any {
	return ownerRefMapFor(monetizeapi.Group+"/"+monetizeapi.Version, monetizeapi.AgentIdentityKind, identity.Name, identity.UID)
}

func ownerRefMapFor(apiVersion, kind, name string, uid types.UID) map[string]any {
	return map[string]any{
		"apiVersion":         apiVersion,
		"kind":               kind,
		"name":               name,
		"uid":                string(uid),
		"controller":         true,
		"blockOwnerDeletion": true,
	}
}

func setCondition(status *monetizeapi.ServiceOfferStatus, conditionType, conditionStatus, reason, message string) {
	now := metav1.NewTime(time.Now().UTC())
	for i := range status.Conditions {
		if status.Conditions[i].Type != conditionType {
			continue
		}
		if status.Conditions[i].Status != conditionStatus {
			status.Conditions[i].LastTransitionTime = now
		}
		status.Conditions[i].Status = conditionStatus
		status.Conditions[i].Reason = reason
		status.Conditions[i].Message = message
		if status.Conditions[i].LastTransitionTime.IsZero() {
			status.Conditions[i].LastTransitionTime = now
		}
		return
	}
	status.Conditions = append(status.Conditions, monetizeapi.Condition{
		Type:               conditionType,
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}

func isConditionTrue(status monetizeapi.ServiceOfferStatus, conditionType string) bool {
	for _, condition := range status.Conditions {
		if condition.Type == conditionType {
			return condition.Status == "True"
		}
	}
	return false
}

func buildActiveRegistrationDocument(owner *monetizeapi.ServiceOffer, offers []*monetizeapi.ServiceOffer, baseURL, agentID string) erc8004.AgentRegistration {
	baseURL = strings.TrimRight(baseURL, "/")
	// Operator-supplied description wins. Only fall back to a controller-
	// generated default when the offer left Spec.Registration.Description
	// empty. The inference-typed default is more specific (names the model),
	// so it preempts the generic default — but neither overrides an explicit
	// operator value.
	description := owner.Spec.Registration.Description
	if description == "" {
		if owner.IsInference() && owner.Spec.Model.Name != "" {
			description = fmt.Sprintf("%s inference via x402 micropayments", owner.Spec.Model.Name)
		} else {
			description = fmt.Sprintf("x402 payment-gated %s service: %s", fallbackOfferType(owner), owner.Name)
		}
	}

	image := owner.Spec.Registration.Image
	if image == "" {
		image = baseURL + "/agent-icon.png"
	}

	services := buildRegistrationServices(owner, offers, baseURL)

	registration := erc8004.AgentRegistration{
		Type:           erc8004.RegistrationType,
		Name:           defaultString(owner.Spec.Registration.Name, owner.Name),
		Description:    description,
		Image:          image,
		Services:       services,
		X402Support:    true,
		Active:         true,
		SupportedTrust: owner.Spec.Registration.SupportedTrust,
	}
	if agentID != "" {
		registration.Registrations = []erc8004.OnChainReg{{
			AgentID:       parseInt64(agentID),
			AgentRegistry: fmt.Sprintf("eip155:%d:%s", erc8004.BaseSepoliaChainID, erc8004.IdentityRegistryBaseSepolia),
		}}
	}
	if metadata := nonEmptyStringMap(owner.Spec.Registration.Metadata); len(metadata) > 0 {
		registration.Metadata = metadata
	}
	if provenance := nonEmptyStringMap(owner.Spec.Provenance); len(provenance) > 0 {
		registration.Provenance = provenance
	}
	return registration
}

func buildTombstoneRegistrationDocument(offer *monetizeapi.ServiceOffer, baseURL, agentID string) erc8004.AgentRegistration {
	registration := buildActiveRegistrationDocument(offer, []*monetizeapi.ServiceOffer{offer}, baseURL, agentID)
	registration.Active = false
	registration.X402Support = false
	registration.Description = fmt.Sprintf("%s (deactivated)", registration.Description)
	return registration
}

func buildRegistrationServices(owner *monetizeapi.ServiceOffer, offers []*monetizeapi.ServiceOffer, baseURL string) []erc8004.ServiceDef {
	baseURL = strings.TrimRight(baseURL, "/")
	type offerKey struct {
		namespace string
		name      string
	}
	seen := map[offerKey]struct{}{}
	ordered := []*monetizeapi.ServiceOffer{}
	add := func(offer *monetizeapi.ServiceOffer, force bool) {
		if offer == nil {
			return
		}
		key := offerKey{namespace: offer.Namespace, name: offer.Name}
		if _, ok := seen[key]; ok {
			return
		}
		if !force && !offerPublishedForRegistration(offer) {
			return
		}
		seen[key] = struct{}{}
		ordered = append(ordered, offer)
	}

	add(owner, true)
	for _, offer := range offers {
		if owner != nil && offer != nil && offer.Namespace == owner.Namespace && offer.Name == owner.Name {
			continue
		}
		add(offer, false)
	}

	services := make([]erc8004.ServiceDef, 0, len(ordered)*2)
	for _, offer := range ordered {
		services = append(services, serviceDefWithDrain(offer, erc8004.ServiceDef{
			Name:     "web",
			Endpoint: baseURL + offer.EffectivePath(),
		}))
		if len(offer.Spec.Registration.Skills) > 0 || len(offer.Spec.Registration.Domains) > 0 {
			services = append(services, erc8004.ServiceDef{
				Name:    "OASF",
				Version: "0.8",
				Skills:  offer.Spec.Registration.Skills,
				Domains: offer.Spec.Registration.Domains,
			})
		}
		for _, service := range offer.Spec.Registration.Services {
			services = append(services, serviceDefWithDrain(offer, erc8004.ServiceDef{
				Name:     service.Name,
				Endpoint: service.Endpoint,
				Version:  service.Version,
			}))
		}
	}
	return services
}

func serviceDefWithDrain(offer *monetizeapi.ServiceOffer, svc erc8004.ServiceDef) erc8004.ServiceDef {
	if offer == nil || !offer.IsDraining() || offer.DrainExpired(time.Now()) {
		return svc
	}
	svc.DrainEndsAt = offer.DrainEndsAt().UTC().Format(time.RFC3339)
	return svc
}

// offerPublishedForRegistration reports whether an offer should appear
// in the operator's ERC-8004 registration document as a live, gated
// service. Draining offers stay in the document with available=false
// so external observers can see the wind-down — this function filters
// them out only after the drain window has fully expired (i.e. the
// HTTPRoute is gone and there is no payment surface to advertise).
func offerPublishedForRegistration(offer *monetizeapi.ServiceOffer) bool {
	if offer == nil || offer.DeletionTimestamp != nil || !offer.Spec.Registration.Enabled {
		return false
	}
	if offer.DrainExpired(time.Now()) {
		return false
	}
	return isConditionTrue(offer.Status, "ModelReady") &&
		isConditionTrue(offer.Status, "UpstreamHealthy") &&
		isConditionTrue(offer.Status, "PaymentGateReady") &&
		isConditionTrue(offer.Status, "RoutePublished")
}

func buildSkillCatalogMarkdown(offers []*monetizeapi.ServiceOffer, baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")

	// Same operationally-ready filter as buildServiceCatalogJSON — keep the
	// two surfaces consistent. An offer that's usable for x402 payments
	// (route published, payment gate active, upstream healthy) appears in
	// both /skill.md and /api/services.json, with the on-chain ERC-8004
	// registration treated as informational metadata rather than a gating
	// signal. See offerOperationallyReady's doc comment for the rationale.
	now := time.Now()
	var ready []*monetizeapi.ServiceOffer
	for _, offer := range offers {
		if offer == nil || offer.DeletionTimestamp != nil {
			continue
		}
		// Drained offers (post-grace-period) have no live route — drop
		// them from the catalog entirely. Draining offers (pre-expiry)
		// stay in the catalog with available=false + drainEndsAt set so
		// buyers can see the wind-down via discovery before the route
		// disappears.
		if offer.DrainExpired(now) {
			continue
		}
		if offerOperationallyReady(offer) {
			ready = append(ready, offer)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].Namespace == ready[j].Namespace {
			return ready[i].Name < ready[j].Name
		}
		return ready[i].Namespace < ready[j].Namespace
	})

	lines := []string{
		"# Obol Stack Service Catalog",
		"",
		fmt.Sprintf("> Generated from %d ready ServiceOffer(s).", len(ready)),
		"",
		fmt.Sprintf("> For machine-readable agent identity, see [/.well-known/agent-registration.json](%s/.well-known/agent-registration.json).", baseURL),
		"",
	}

	if len(ready) == 0 {
		lines = append(lines, "**No services currently available.**", "")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "## Services", "")
	lines = append(lines, "| Service | Type | Model | Price | Status | Endpoint |")
	lines = append(lines, "|---------|------|-------|-------|--------|----------|")
	for _, offer := range ready {
		modelName := offer.Spec.Model.Name
		if modelName == "" {
			modelName = "—"
		}
		status := "—"
		if offer.IsDraining() {
			status = fmt.Sprintf("draining · ends `%s`", offer.DrainEndsAt().UTC().Format(time.RFC3339))
		}
		lines = append(lines, fmt.Sprintf(
			"| [%s](#%s) | %s | %s | %s | %s | `%s%s` |",
			offer.Name,
			offer.Name,
			fallbackOfferType(offer),
			modelName,
			describeOfferPrice(offer),
			status,
			baseURL,
			offer.EffectivePath(),
		))
	}
	lines = append(lines, "", "## Service Details", "")
	for _, offer := range ready {
		modelName := offer.Spec.Model.Name
		lines = append(lines, fmt.Sprintf("### %s", offer.Name))
		lines = append(lines, fmt.Sprintf("- **Endpoint**: `%s%s`", baseURL, offer.EffectivePath()))
		lines = append(lines, fmt.Sprintf("- **Type**: %s", fallbackOfferType(offer)))
		if modelName != "" {
			lines = append(lines, fmt.Sprintf("- **Model**: %s", modelName))
		}
		lines = append(lines, fmt.Sprintf("- **Price**: %s", describeOfferPrice(offer)))
		lines = append(lines, fmt.Sprintf("- **Pay To**: `%s`", firstNonEmpty(offer.Spec.Payment.PayTo, "—")))
		lines = append(lines, fmt.Sprintf("- **Network**: %s", firstNonEmpty(offer.Spec.Payment.Network, "—")))
		if offer.IsDraining() {
			lines = append(lines, fmt.Sprintf("- **Drain ends at**: %s", offer.DrainEndsAt().UTC().Format(time.RFC3339)))
		}
		description := offer.Spec.Registration.Description
		if description == "" {
			description = fmt.Sprintf("x402 payment-gated %s service", fallbackOfferType(offer))
		}
		lines = append(lines, fmt.Sprintf("- **Description**: %s", description), "")
	}

	return strings.Join(lines, "\n")
}

// offerOperationallyReady reports whether an offer is usable for x402
// payments today. This is intentionally LOOSER than the controller's
// Ready=True condition: ModelReady + UpstreamHealthy + PaymentGateReady
// + RoutePublished are sufficient. Registered is NOT in the AND. The
// reasoning: ERC-8004 on-chain registration is publication metadata, not
// operational readiness — an offer with the route published, payment gate
// active, and upstream healthy serves buyers correctly regardless of
// whether the on-chain identity has been minted yet.
//
// Used by the storefront catalog (and the skill catalog) so an offer that
// is functionally usable doesn't disappear from the operator's own
// dashboard just because the agent wallet hasn't been funded with gas
// yet. Callers should set ServiceCatalogEntry.RegistrationPending = true
// when the offer's Registered condition is False with reason
// AwaitingExternalRegistration, so storefront UIs can badge it.
func offerOperationallyReady(offer *monetizeapi.ServiceOffer) bool {
	if offer == nil {
		return false
	}
	// Backwards-compatible shortcut: the aggregate Ready=True implies all
	// the per-condition gates by construction (see controller.go's `ready`
	// computation), and existing tests / external callers that only emit
	// the aggregate signal still want their offers to appear.
	if isConditionTrue(offer.Status, "Ready") {
		return true
	}
	// Fine-grained operational readiness: the four per-condition gates
	// that make the offer usable today. Registered is intentionally NOT
	// in this AND — see the doc comment on this function and on
	// buildServiceCatalogJSON for the rationale.
	return isConditionTrue(offer.Status, "ModelReady") &&
		isConditionTrue(offer.Status, "UpstreamHealthy") &&
		isConditionTrue(offer.Status, "PaymentGateReady") &&
		isConditionTrue(offer.Status, "RoutePublished")
}

// offerAwaitingRegistration reports whether an offer is operationally
// ready but has its on-chain ERC-8004 registration still pending. Used to
// flip ServiceCatalogEntry.RegistrationPending so storefront UIs can show
// a "registration pending" badge alongside the usable offer.
func offerAwaitingRegistration(offer *monetizeapi.ServiceOffer) bool {
	if offer == nil {
		return false
	}
	for _, c := range offer.Status.Conditions {
		if c.Type == "Registered" && c.Status == "False" && c.Reason == "AwaitingExternalRegistration" {
			return true
		}
	}
	return false
}

// buildServiceCatalogJSON returns a JSON array of operationally-ready
// ServiceOffers for the public storefront feed (/api/services.json).
//
// The filter is operationally-ready (route published, payment gate
// active, upstream healthy) rather than the stricter controller
// Ready=True (which also requires Registered=True). Excluding offers
// whose only False condition is AwaitingExternalRegistration made
// operators' own seller dashboards mysteriously empty after `obol stack
// up` until they funded the agent wallet and ran `obol sell register`.
// That UX failed the "all paid services come back automatically" promise
// of the stack-up resume feature.
func buildServiceCatalogJSON(offers []*monetizeapi.ServiceOffer, baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")

	now := time.Now()
	var ready []*monetizeapi.ServiceOffer
	for _, offer := range offers {
		if offer == nil || offer.DeletionTimestamp != nil {
			continue
		}
		// Drained offers (post-grace-period) have no live route — drop
		// them from the catalog entirely. Draining offers (pre-expiry)
		// stay in the catalog with available=false + drainEndsAt set so
		// buyers can react before the route disappears.
		if offer.DrainExpired(now) {
			continue
		}
		if offerOperationallyReady(offer) {
			ready = append(ready, offer)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		return ready[i].Name < ready[j].Name
	})

	services := make([]schemas.ServiceCatalogEntry, 0, len(ready))
	for _, offer := range ready {
		desc := offer.Spec.Registration.Description
		if desc == "" {
			desc = fmt.Sprintf("x402 payment-gated %s service", fallbackOfferType(offer))
		}
		// type=agent offers leave spec.model empty by design (the model
		// lives on the linked Agent). Fall back to the controller's
		// resolved view so the storefront can display it.
		modelName := offer.Spec.Model.Name
		if modelName == "" && offer.Status.AgentResolution != nil {
			modelName = offer.Status.AgentResolution.Model
		}

		drainEndsAt := ""
		if offer.IsDraining() {
			drainEndsAt = offer.DrainEndsAt().UTC().Format(time.RFC3339)
		}

		svc := schemas.ServiceCatalogEntry{
			Name:                offer.Name,
			Namespace:           offer.Namespace,
			Type:                fallbackOfferType(offer),
			Model:               modelName,
			Endpoint:            baseURL + offer.EffectivePath(),
			Price:               describeOfferPrice(offer),
			PayTo:               offer.Spec.Payment.PayTo,
			Network:             offer.Spec.Payment.Network,
			Description:         desc,
			IsDemo:              offer.Namespace == "demo",
			RegistrationPending: offerAwaitingRegistration(offer),
			DrainEndsAt:         drainEndsAt,
		}

		raw, unit := offerPriceRawAndUnit(offer)
		svc.PriceRaw = raw
		svc.PriceUnit = unit

		caip2, chainID := caip2ForNetwork(offer.Spec.Payment.Network)
		svc.CAIP2Network = caip2
		svc.ChainID = chainID

		asset := offerAssetJSON(offer)
		if asset != nil {
			svc.Asset = asset
			if raw != "" && asset.Decimals > 0 {
				svc.PriceAtomicUnits = decimalToAtomicString(raw, int(asset.Decimals))
			}
		}

		services = append(services, svc)
	}

	out, err := json.MarshalIndent(services, "", "  ")
	if err != nil {
		return "[]"
	}
	return string(out)
}

// offerPriceRawAndUnit returns the raw decimal price string and which slot it
// occupies in the price table. Only one of perRequest / perMTok / perHour is
// expected to be set on a given offer.
func offerPriceRawAndUnit(offer *monetizeapi.ServiceOffer) (string, string) {
	switch {
	case offer.Spec.Payment.Price.PerRequest != "":
		return offer.Spec.Payment.Price.PerRequest, "perRequest"
	case offer.Spec.Payment.Price.PerMTok != "":
		return offer.Spec.Payment.Price.PerMTok, "perMTok"
	case offer.Spec.Payment.Price.PerHour != "":
		return offer.Spec.Payment.Price.PerHour, "perHour"
	default:
		return "", ""
	}
}

// offerAssetJSON resolves the settlement asset block. If the offer carries an
// explicit asset, it is used verbatim. If only the network is set, defaults
// for USDC on that chain are filled in (this matches the verifier's behavior
// when the seller did not pass --token).
func offerAssetJSON(offer *monetizeapi.ServiceOffer) *schemas.ServiceCatalogAsset {
	a := offer.Spec.Payment.Asset
	if a.Address == "" && a.Symbol == "" && a.EIP712Name == "" {
		// No explicit asset — fall back to the chain's default USDC entry.
		if def, ok := defaultUSDCForNetwork(offer.Spec.Payment.Network); ok {
			return &def
		}
		return nil
	}
	out := &schemas.ServiceCatalogAsset{
		Address:        a.Address,
		Symbol:         a.Symbol,
		Decimals:       a.Decimals,
		TransferMethod: a.TransferMethod,
	}
	if a.EIP712Name != "" || a.EIP712Version != "" {
		out.EIP712Domain = &schemas.ServiceCatalogEIP712Domain{Name: a.EIP712Name, Version: a.EIP712Version}
	}
	if def, ok := defaultUSDCForNetwork(offer.Spec.Payment.Network); ok {
		// Backfill any unset fields from chain defaults so consumers always
		// see a complete asset block when the network is known.
		if out.Address == "" {
			out.Address = def.Address
		}
		if out.Symbol == "" {
			out.Symbol = def.Symbol
		}
		if out.Decimals == 0 {
			out.Decimals = def.Decimals
		}
		if out.TransferMethod == "" {
			out.TransferMethod = def.TransferMethod
		}
		if out.EIP712Domain == nil {
			out.EIP712Domain = def.EIP712Domain
		}
	}
	return out
}

// caip2ForNetwork maps a chain name (or CAIP-2 string) to (CAIP-2, chainID).
// Returns ("", 0) when the network is unrecognized — the catalog still
// publishes the offer, just without these convenience fields.
func caip2ForNetwork(network string) (string, int64) {
	if strings.HasPrefix(network, "eip155:") {
		parts := strings.SplitN(network, ":", 2)
		if len(parts) == 2 {
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil {
				return network, id
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "base", "base-mainnet":
		return "eip155:8453", 8453
	case "base-sepolia":
		return "eip155:84532", 84532
	case "ethereum", "ethereum-mainnet", "mainnet":
		return "eip155:1", 1
	case "polygon", "polygon-mainnet":
		return "eip155:137", 137
	case "polygon-amoy":
		return "eip155:80002", 80002
	case "avalanche", "avalanche-mainnet":
		return "eip155:43114", 43114
	case "avalanche-fuji":
		return "eip155:43113", 43113
	case "arbitrum", "arbitrum-one":
		return "eip155:42161", 42161
	case "arbitrum-sepolia":
		return "eip155:421614", 421614
	default:
		return "", 0
	}
}

// defaultUSDCForNetwork returns the canonical USDC settlement asset for a
// chain when the seller did not specify an explicit asset. Mirrors the
// verifier's chain → asset defaults so /api/services.json stays consistent
// with what the 402 response advertises.
func defaultUSDCForNetwork(network string) (schemas.ServiceCatalogAsset, bool) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "base", "base-mainnet":
		return schemas.ServiceCatalogAsset{
			Address:        "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
			Symbol:         "USDC",
			Decimals:       6,
			TransferMethod: "eip3009",
			EIP712Domain:   &schemas.ServiceCatalogEIP712Domain{Name: "USD Coin", Version: "2"},
		}, true
	case "base-sepolia":
		return schemas.ServiceCatalogAsset{
			Address:        "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			Symbol:         "USDC",
			Decimals:       6,
			TransferMethod: "eip3009",
			// Empirically Base Sepolia USDC's signing domain name is "USDC",
			// while the contract's name() returns "USD Coin". Keep "USDC"
			// here — buy.py signs with this and the facilitator settles.
			EIP712Domain: &schemas.ServiceCatalogEIP712Domain{Name: "USDC", Version: "2"},
		}, true
	case "ethereum", "ethereum-mainnet", "mainnet":
		return schemas.ServiceCatalogAsset{
			Address:        "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
			Symbol:         "USDC",
			Decimals:       6,
			TransferMethod: "eip3009",
			EIP712Domain:   &schemas.ServiceCatalogEIP712Domain{Name: "USD Coin", Version: "2"},
		}, true
	default:
		return schemas.ServiceCatalogAsset{}, false
	}
}

// decimalToAtomicString converts a decimal token amount (e.g. "0.001") to
// atomic units using big.Float to avoid floating-point truncation. Returns
// "" on parse error so callers can omit the field.
func decimalToAtomicString(amount string, decimals int) string {
	if amount == "" || decimals < 0 {
		return ""
	}
	parsed, _, err := big.ParseFloat(amount, 10, 128, big.ToNearestEven)
	if err != nil || parsed == nil {
		return ""
	}
	multiplier := new(big.Float).SetPrec(128).SetInt(
		new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil),
	)
	atomic := new(big.Float).SetPrec(128).Mul(parsed, multiplier)
	atomic.Add(atomic, new(big.Float).SetPrec(128).SetFloat64(0.5))
	out, _ := atomic.Int(nil)
	if out == nil {
		return ""
	}
	return out.String()
}

func describeOfferPrice(offer *monetizeapi.ServiceOffer) string {
	// Source the symbol from (in order): explicit asset metadata on the offer,
	// the resolved chain-default settlement asset, hard-coded "USDC" only as
	// the last-resort fallback for unknown chains. Mislabeling OBOL-priced
	// services as "USDC" on the discovery surfaces (storefront / skill.md)
	// caused buyers to queue up the wrong asset on rc7-rc9.
	symbol := offer.Spec.Payment.Asset.Symbol
	if symbol == "" {
		if a := offerAssetJSON(offer); a != nil && a.Symbol != "" {
			symbol = a.Symbol
		}
	}
	if symbol == "" {
		symbol = "USDC"
	}
	switch {
	case offer.Spec.Payment.Price.PerRequest != "":
		return offer.Spec.Payment.Price.PerRequest + " " + symbol + "/request"
	case offer.Spec.Payment.Price.PerMTok != "":
		return offer.Spec.Payment.Price.PerMTok + " " + symbol + "/MTok"
	case offer.Spec.Payment.Price.PerHour != "":
		return offer.Spec.Payment.Price.PerHour + " " + symbol + "/hour"
	default:
		return "—"
	}
}

func marshalRegistrationDocument(document erc8004.AgentRegistration) (string, string, error) {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", "", err
	}
	sum := md5.Sum(data)
	return string(data), fmt.Sprintf("%x", sum[:8]), nil
}

func registrationDataURL(document erc8004.AgentRegistration) (string, error) {
	data, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	return "data:application/json," + url.PathEscape(string(data)), nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func fallbackOfferType(offer *monetizeapi.ServiceOffer) string {
	if offer.Spec.Type != "" {
		return offer.Spec.Type
	}
	return "http"
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func nonEmptyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		result[key] = value
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
