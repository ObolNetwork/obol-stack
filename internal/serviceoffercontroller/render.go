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

	"github.com/ObolNetwork/obol-stack/internal/buyprompts"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

const (
	skillCatalogNamespace     = "x402"
	skillCatalogConfigMapName = "obol-skill-md"
	skillCatalogRouteName     = "obol-skill-md-route"
	servicesJSONRouteName     = "obol-services-json-route"
	openAPIRouteName          = "obol-openapi-route"
	apiDocsRouteName          = "obol-api-docs-route"

	// catalogHeadersMiddlewareName is the Traefik headers Middleware attached
	// to the public catalog HTTPRoutes (/skill.md, /openapi.json, /api,
	// /api/services.json). The busybox httpd serving those files cannot set
	// custom response headers, so CORS + caching are applied at the gateway.
	catalogHeadersMiddlewareName = "obol-catalog-headers"
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

func buildSkillCatalogConfigMap(content, servicesJSON, openAPIJSON, apiDocsHTML string, bundles []offerBundleFile) *unstructured.Unstructured {
	data := map[string]any{
		"skill.md":      content,
		"services.json": servicesJSON,
		"openapi.json":  openAPIJSON,
		"api.html":      apiDocsHTML,
		"httpd.conf":    ".md:text/markdown\n.json:application/json\n.html:text/html\n",
	}
	for _, f := range bundles {
		data[f.Key] = f.Content
	}
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
			"data": data,
		},
	}
}

// skillCatalogVolumeItems projects the ConfigMap keys into the httpd's /www
// tree: the four aggregate documents plus one file per hostname-offer
// bundle entry (offers/<ns>/<name>/…).
func skillCatalogVolumeItems(bundles []offerBundleFile) []any {
	items := []any{
		map[string]any{"key": "skill.md", "path": "skill.md"},
		map[string]any{"key": "services.json", "path": "api/services.json"},
		map[string]any{"key": "openapi.json", "path": "openapi.json"},
		// busybox httpd resolves /api/ → /api/index.html, so the
		// Scalar shell sits at api/index.html. The /api Exact
		// HTTPRoute also matches the trailing-slash variant so the
		// resolver kicks in either way.
		map[string]any{"key": "api.html", "path": "api/index.html"},
	}
	for _, f := range bundles {
		items = append(items, map[string]any{"key": f.Key, "path": f.Path})
	}
	return items
}

func buildSkillCatalogDeployment(contentHash string, bundles []offerBundleFile) *unstructured.Unstructured {
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
									"name":  skillCatalogConfigMapName,
									"items": skillCatalogVolumeItems(bundles),
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

// buildCatalogHeadersMiddleware renders the Traefik headers Middleware for
// the public discovery surfaces. These are read-only public documents served
// without credentials, so a wildcard CORS origin is correct — browser-based
// buyers, dashboards, and aggregators must be able to fetch them cross-origin.
// Cache-Control keeps CDN/browser refetch pressure off the busybox httpd
// while staying short enough (5 min) that catalog updates propagate quickly.
//
// Deliberately NOT attached to the /services/* paid routes (the 402/paid
// path has its own header semantics) nor to the ERC-8004
// /.well-known/agent-registration.json routes (those live in per-agent
// namespaces, where an ExtensionRef cannot reference this x402-namespace
// Middleware).
func buildCatalogHeadersMiddleware() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "Middleware",
			"metadata": map[string]any{
				"name":      catalogHeadersMiddlewareName,
				"namespace": skillCatalogNamespace,
				"labels": map[string]any{
					"obol.org/managed-by": "serviceoffer-controller",
				},
			},
			"spec": map[string]any{
				"headers": map[string]any{
					"accessControlAllowOriginList": []any{"*"},
					"accessControlAllowMethods":    []any{"GET", "OPTIONS"},
					"customResponseHeaders": map[string]any{
						"Cache-Control": "public, max-age=300",
					},
				},
			},
		},
	}
}

// catalogHeadersFilters is the HTTPRoute rule filter list that attaches the
// catalog headers Middleware — same ExtensionRef mechanism the x402
// ForwardAuth Middleware uses on gated routes.
func catalogHeadersFilters() []any {
	return []any{
		map[string]any{
			"type": "ExtensionRef",
			"extensionRef": map[string]any{
				"group": "traefik.io",
				"kind":  "Middleware",
				"name":  catalogHeadersMiddlewareName,
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
						"filters": catalogHeadersFilters(),
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

// buildOpenAPIHTTPRoute exposes the aggregate OpenAPI 3.1 document at the
// stable public path /openapi.json. The route deliberately omits a
// hostnames restriction so it's reachable both on the local cluster
// (obol.stack:8080) AND through the public Cloudflare tunnel — the spec
// is meant to be discoverable by any client. /openapi.json contains no
// secret material (payment addresses + chain selectors are also published
// on /skill.md and ERC-8004); future "tighten all public routes" cleanups
// must NOT add a hostnames filter here.
func buildOpenAPIHTTPRoute() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]any{
				"name":      openAPIRouteName,
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
									"value": "/openapi.json",
								},
							},
						},
						"filters": catalogHeadersFilters(),
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

// buildAPIDocsHTTPRoute exposes the Scalar UI shell at /api and /api/.
// Two Exact rules are needed because Gateway API's Exact matcher does not
// normalize trailing slashes; busybox httpd resolves /api/ to
// api/index.html inside the mounted ConfigMap volume.
//
// /api/services.json (also Exact) is registered as its own HTTPRoute and
// continues to win the path because Exact-vs-Exact is decided by literal
// match — /api vs /api/services.json never overlap.
//
// Same hostnames posture as /openapi.json: explicitly tunnel-reachable.
// Do not add a hostnames filter without rethinking the discovery story.
func buildAPIDocsHTTPRoute() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]any{
				"name":      apiDocsRouteName,
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
									"value": "/api",
								},
							},
							map[string]any{
								"path": map[string]any{
									"type":  "Exact",
									"value": "/api/",
								},
							},
						},
						"filters": catalogHeadersFilters(),
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
						"filters": catalogHeadersFilters(),
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

// hasLimits reports whether the offer wants protection middleware.
func hasLimits(offer *monetizeapi.ServiceOffer) bool {
	return offer.Spec.Limits.MaxInFlight > 0 || offer.Spec.Limits.RPS > 0
}

// buildLimitsMiddleware renders the Traefik protection middleware for
// spec.limits: inFlightReq (concurrency cap — the unbounded-concurrency
// hole on paid agents is pentest-proven) and/or rateLimit. Lives in the
// offer's namespace because Gateway API ExtensionRef resolves there.
func buildLimitsMiddleware(offer *monetizeapi.ServiceOffer) *unstructured.Unstructured {
	spec := map[string]any{}
	if offer.Spec.Limits.MaxInFlight > 0 {
		spec["inFlightReq"] = map[string]any{"amount": offer.Spec.Limits.MaxInFlight}
	}
	if offer.Spec.Limits.RPS > 0 {
		spec["rateLimit"] = map[string]any{
			"average": offer.Spec.Limits.RPS,
			"burst":   offer.Spec.Limits.RPS * 2,
		}
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "Middleware",
			"metadata": map[string]any{
				"name":            limitsMiddlewareName(offer.Name),
				"namespace":       offer.Namespace,
				"ownerReferences": []any{ownerRefMap(offer)},
			},
			"spec": spec,
		},
	}
}

func limitsMiddlewareName(offerName string) string {
	return safeName("so-", offerName, "-limits")
}

// limitsFilter is the ExtensionRef filter attached to gated rules when
// spec.limits is set.
func limitsFilter(offer *monetizeapi.ServiceOffer) map[string]any {
	return map[string]any{
		"type": "ExtensionRef",
		"extensionRef": map[string]any{
			"group": "traefik.io",
			"kind":  "Middleware",
			"name":  limitsMiddlewareName(offer.Name),
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
					sharedOriginRule(offer),
				},
			},
		},
	}
	return obj
}

// buildHostHTTPRoute renders the dedicated-origin route for a hostname-bound
// offer. Topology (proven live before being generalized here — see
// docs/proposals/multistore-storefront-routing.md appendix):
//
//   - Exact /, /openapi.json, /.well-known/x402 → the catalog httpd, with
//     full-path rewrites into the offer's generated bundle files. These are
//     structurally free — they never touch the payment gate.
//   - PathPrefix / → x402-verifier, with the public path rewritten into the
//     shared /services/<name> path-world (so the verifier's route table —
//     gates, prices, carve-outs — applies unchanged) and X-Forwarded-Host
//     pinned to the offer hostname (SIWX domain binding + resource URLs).
//
// Gateway API ranks Exact matches above PathPrefix, so the discovery rules
// win their paths and everything else reaches the gate.
func buildHostHTTPRoute(offer *monetizeapi.ServiceOffer) *unstructured.Unstructured {
	dir := "/" + offerBundleDir(offer)
	exactTo := func(publicPath, file string) map[string]any {
		return map[string]any{
			"matches": []any{
				map[string]any{"path": map[string]any{"type": "Exact", "value": publicPath}},
			},
			"filters": []any{
				map[string]any{
					"type": "URLRewrite",
					"urlRewrite": map[string]any{
						"path": map[string]any{"type": "ReplaceFullPath", "replaceFullPath": dir + "/" + file},
					},
				},
			},
			"backendRefs": []any{
				map[string]any{"name": skillCatalogConfigMapName, "namespace": skillCatalogNamespace, "port": int64(8080)},
			},
		}
	}

	catchallFilters := []any{
		map[string]any{
			"type": "URLRewrite",
			"urlRewrite": map[string]any{
				"path": map[string]any{
					"type":               "ReplacePrefixMatch",
					"replacePrefixMatch": strings.TrimSuffix(offer.EffectivePath(), "/"),
				},
			},
		},
		map[string]any{
			"type": "RequestHeaderModifier",
			"requestHeaderModifier": map[string]any{
				"set": []any{
					map[string]any{"name": "X-Forwarded-Host", "value": offer.Spec.Hostname},
					map[string]any{"name": "X-Forwarded-Proto", "value": "https"},
				},
			},
		},
	}
	if hasLimits(offer) {
		catchallFilters = append(catchallFilters, limitsFilter(offer))
	}

	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]any{
				"name":            hostChildName(offer.Name),
				"namespace":       offer.Namespace,
				"ownerReferences": []any{ownerRefMap(offer)},
			},
			"spec": map[string]any{
				"hostnames": []any{offer.Spec.Hostname},
				"parentRefs": []any{
					map[string]any{
						"name":        "traefik-gateway",
						"namespace":   "traefik",
						"sectionName": "web",
					},
				},
				"rules": []any{
					exactTo("/", "index.html"),
					exactTo("/openapi.json", "openapi.json"),
					exactTo("/.well-known/x402", "x402.json"),
					map[string]any{
						"matches": []any{
							map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/"}},
						},
						"filters": catchallFilters,
						"backendRefs": []any{
							map[string]any{"name": "x402-verifier", "namespace": "x402", "port": int64(8080)},
						},
					},
				},
			},
		},
	}
}

// sharedOriginRule is the /services/<name> PathPrefix rule → verifier,
// with the protection middleware attached when spec.limits is set.
func sharedOriginRule(offer *monetizeapi.ServiceOffer) map[string]any {
	rule := map[string]any{
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
	}
	if hasLimits(offer) {
		rule["filters"] = []any{limitsFilter(offer)}
	}
	return rule
}

func hostChildName(offerName string) string {
	return safeName("so-", offerName, "-host")
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
					// Hostname-bound offers backend their discovery bundle
					// (Exact / + /openapi.json + /.well-known/x402 rules)
					// to the catalog httpd in this namespace.
					map[string]any{
						"group": "",
						"kind":  "Service",
						"name":  skillCatalogConfigMapName,
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

func buildSkillCatalogMarkdown(offers []*monetizeapi.ServiceOffer, baseURL string, explicit *schemas.StorefrontProfile) string {
	baseURL = strings.TrimRight(baseURL, "/")
	profile := storefront.ResolvePublished(explicit, baseURL)

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
		// stay in the catalog with draining status + drainEndsAt so buyers
		// can see the wind-down via discovery before the route disappears.
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
		fmt.Sprintf("# %s Service Catalog", profile.DisplayName),
		"",
		fmt.Sprintf("> Generated from %d ready ServiceOffer(s). Every service below is gated by [x402](https://www.x402.org) micropayments — no API key, no signup, no subscription.", len(ready)),
		"",
		"> **Machine-readable:** " +
			fmt.Sprintf("OpenAPI 3.1 (Swagger) at [`%s/openapi.json`](%s/openapi.json) · ", baseURL, baseURL) +
			fmt.Sprintf("catalog feed at [`%s/api/services.json`](%s/api/services.json) · ", baseURL, baseURL) +
			fmt.Sprintf("agent identity at [`%s/.well-known/agent-registration.json`](%s/.well-known/agent-registration.json).", baseURL, baseURL),
		"",
	}

	lines = append(lines, skillCatalogHowToPay(baseURL)...)

	if len(ready) == 0 {
		lines = append(lines, "## Services", "", "**No services currently available.**", "")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "## Services", "")
	lines = append(lines, "| Service | Type | Model | Pay with | Status | Endpoint |")
	lines = append(lines, "|---------|------|-------|----------|--------|----------|")
	for _, offer := range ready {
		modelName := offer.Spec.Model.Name
		if modelName == "" {
			modelName = "—"
		}
		status := "available"
		if offer.IsDraining() {
			status = fmt.Sprintf("draining · ends `%s`", offer.DrainEndsAt().UTC().Format(time.RFC3339))
		}
		tableEndpoint := baseURL + offer.EffectivePath()
		if origin := offer.EffectiveOrigin(); origin != "" {
			tableEndpoint = origin
		}
		lines = append(lines, fmt.Sprintf(
			"| [%s](#%s) | %s | %s | %s | %s | `%s` |",
			offer.Name,
			offer.Name,
			fallbackOfferType(offer),
			modelName,
			describeOfferPaymentsInline(offer),
			status,
			tableEndpoint,
		))
	}
	lines = append(lines, "", "## Service Details", "")
	for _, offer := range ready {
		modelName := offer.Spec.Model.Name
		endpoint := baseURL + offer.EffectivePath()
		if origin := offer.EffectiveOrigin(); origin != "" {
			endpoint = origin
		}
		lines = append(lines, fmt.Sprintf("### %s", offer.Name))
		lines = append(lines, fmt.Sprintf("- **Endpoint**: `%s`", endpoint))
		lines = append(lines, fmt.Sprintf("- **Call**: %s", offerCallHint(offer, endpoint)))
		if anchor := openAPIDocsAnchorForOffer(offer); anchor != "" {
			lines = append(lines, fmt.Sprintf(
				"- **API docs**: [%s%s](%s%s) — schema path `%s` in [openapi.json](%s/openapi.json)",
				baseURL, anchor, baseURL, anchor,
				openAPIPrimaryPathForOffer(offer), baseURL,
			))
		}
		lines = append(lines, fmt.Sprintf("- **Type**: %s", fallbackOfferType(offer)))
		if modelName != "" {
			lines = append(lines, fmt.Sprintf("- **Model**: %s", modelName))
		}
		payments := offer.EffectivePayments()
		if len(payments) == 1 {
			lines = append(lines, fmt.Sprintf("- **Payment**: %s", describePaymentDetail(payments[0])))
		} else {
			lines = append(lines, "- **Payment options** (pick one):")
			for i := range payments {
				lines = append(lines, fmt.Sprintf("  %d. %s", i+1, describePaymentDetail(payments[i])))
			}
		}
		lines = append(lines, skillCatalogRouteLines(offer, endpoint)...)
		if offer.Spec.Async.Enabled {
			access := "results are gated to the paying wallet (SIWX sign-in) or the `jobToken` from the 202 body"
			if offer.Spec.Async.EffectiveResultVisibility() == monetizeapi.ResultVisibilityPublic {
				access = "results are public (the unguessable job id is the capability)"
			}
			lines = append(lines, fmt.Sprintf(
				"- **Delivery**: async — paid calls return `202` with `{jobId, statusUrl, resultUrl, jobToken}`; poll `%s/jobs/<jobId>` (free) until `state=complete`, then fetch the result — %s. Optional `{\"callbackUrl\": ...}` in a JSON body registers a completion webhook.",
				endpoint, access))
		}
		if offer.IsDraining() {
			lines = append(lines, fmt.Sprintf("- **Drain ends at**: %s", offer.DrainEndsAt().UTC().Format(time.RFC3339)))
		}
		description := offer.Spec.Registration.Description
		if description == "" {
			description = fmt.Sprintf("x402 payment-gated %s service", fallbackOfferType(offer))
		}
		lines = append(lines, fmt.Sprintf("- **Description**: %s", description), "")
		lines = append(lines, skillCatalogTryIt(offer, endpoint)...)
	}

	return strings.Join(lines, "\n")
}

// skillCatalogHowToPay returns the self-contained "How to pay" section. It
// is written so any LLM agent — not just one running on Obol Stack — can
// pay these endpoints by following the x402 v2 loop, without first reading
// any external doc. baseURL points the reader at the machine-readable specs.
func skillCatalogHowToPay(baseURL string) []string {
	return []string{
		"## How to pay (x402)",
		"",
		"Calling any endpoint below follows the same five steps. No wallet onboarding " +
			"beyond holding the settlement token — payment is per-request and gasless.",
		"",
		"1. **Call the endpoint with no payment.** You get `402 Payment Required` with a JSON " +
			"body whose `accepts[]` array lists every payment the operator will take — each entry " +
			"carries the price in atomic units (`amount`; legacy sellers may use `maxAmountRequired`), " +
			"the CAIP-2 chain id (`network`), the settlement token contract (`asset`), the recipient " +
			"(`payTo`), and the transfer scheme.",
		"2. **Pick one `accepts[]` entry** whose token + chain you can pay on. Sellers may advertise " +
			"several (e.g. USDC on Base *or* OBOL on Ethereum); they are alternatives, you satisfy one.",
		"3. **Sign an authorization** matching that entry — an EIP-3009 `TransferWithAuthorization` " +
			"(USDC) or a Permit2 witness (most other ERC-20s, signalled by `extra.assetTransferMethod`). " +
			"This is an off-chain signature; **no ETH/gas needed** — the operator's facilitator submits " +
			"and pays for the on-chain settlement.",
		"4. **Retry the identical request** with the signed payload base64-encoded in the `X-PAYMENT` header.",
		"5. **On success** you get your `200` plus settlement metadata in the `X-PAYMENT-RESPONSE` header. " +
			"For chat-completions endpoints, pass `\"stream\": true` for long-running calls.",
		"",
		fmt.Sprintf("**Exact request shapes:** the OpenAPI 3.1 document at [`%s/openapi.json`](%s/openapi.json) "+
			"describes every operation's path, method, request/response body, and per-operation pricing "+
			"(`x-payment-info`). Load it into any OpenAPI-aware client to generate a typed caller.", baseURL, baseURL),
		"",
		"**Already on Obol Stack?** The `buy-x402` skill automates the whole loop: " +
			"`buy.py pay <endpoint>` for one-shot calls (add `--token <SYMBOL>` / `--network <chain>` to " +
			"choose among multi-currency options), or `buy.py buy <name> --endpoint <url> --model <id>` to " +
			"pre-authorize a batch of paid inference.",
		"",
	}
}

// catalogModelName resolves the model id a buyer should put in a paid
// chat-completions body. type=agent offers leave spec.model empty by design
// (the model lives on the linked Agent), so fall back to the controller's
// resolved view. Shared by /api/services.json and the /skill.md worked
// examples so both surfaces advertise the same id.
func catalogModelName(offer *monetizeapi.ServiceOffer) string {
	if offer == nil {
		return ""
	}
	if offer.Spec.Model.Name != "" {
		return offer.Spec.Model.Name
	}
	if offer.Status.AgentResolution != nil {
		return offer.Status.AgentResolution.Model
	}
	return ""
}

// skillCatalogTryIt renders the per-offer "Try it" subsection: one curl that
// probes the 402 pricing, and one worked paid request. The paid example for
// chat-shaped offers is buyprompts.Build's Example — the exact same bytes
// /api/services.json publishes in the entry's buy.example — so the two
// surfaces cannot drift. Agent buyers convert off copy-paste, not prose.
func skillCatalogTryIt(offer *monetizeapi.ServiceOffer, endpoint string) []string {
	// Route-table offers: probe + pay against the primary paid route, not
	// the offer root (which may not be served at all when the table has no
	// catch-all).
	target := endpoint
	if rt, ok := primaryPaidRoute(offer); ok {
		target = endpoint + openAPIRelPathForRoute(rt.Path)
	}
	lines := []string{
		"#### Try it",
		"",
		"Probe the price (no payment; the `402` body carries the signable `accepts[]` requirements):",
		"",
		"```bash",
		fmt.Sprintf("curl -i %s", target),
		"```",
		"",
	}
	block := buyprompts.Build(buyprompts.Input{
		Type:  fallbackOfferType(offer),
		URL:   target,
		Model: catalogModelName(offer),
	})
	if block.Example != "" {
		// inference/agent: OpenAI-style chat-completions with the real model id.
		lines = append(lines,
			"Then sign one `accepts[]` entry — see [How to pay (x402)](#how-to-pay-x402) — and send a paid request:",
			"",
			"```",
			block.Example,
			"```",
			"",
		)
	} else {
		// http/fine-tuning: retry the gated path itself with the payment attached.
		lines = append(lines,
			"Then sign one `accepts[]` entry — see [How to pay (x402)](#how-to-pay-x402) — and retry the identical request with the payment attached:",
			"",
			"```bash",
			fmt.Sprintf("curl -i %s -H \"X-PAYMENT: <base64-signed-authorization>\"", endpoint),
			"```",
			"",
		)
	}
	return lines
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
// offerCategory returns the storefront grouping category for an offer.
// spec.listing.category is the source of truth; demo services are just
// category="demo" like any other section. For backward compatibility with
// offers created before listing.category existed, legacy demo signals
// (namespace "demo", or the obol.org/demo=true label set on agent-backed
// demos whose offer must live in agent-<name>) still map to "demo".
func offerCategory(offer *monetizeapi.ServiceOffer) string {
	if offer == nil {
		return ""
	}
	if c := strings.TrimSpace(offer.Spec.Listing.Category); c != "" {
		return c
	}
	if offer.Namespace == "demo" || offer.Labels["obol.org/demo"] == "true" {
		return "demo"
	}
	return ""
}

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

// buildServiceCatalogJSON returns the public /api/services.json envelope:
// seller branding plus operationally-ready ServiceOffers.
//
// The filter is operationally-ready (route published, payment gate
// active, upstream healthy) rather than the stricter controller
// Ready=True (which also requires Registered=True). Excluding offers
// whose only False condition is AwaitingExternalRegistration made
// operators' own seller dashboards mysteriously empty after `obol stack
// up` until they funded the agent wallet and ran `obol sell register`.
// That UX failed the "all paid services come back automatically" promise
// of the stack-up resume feature.
func buildServiceCatalogJSON(offers []*monetizeapi.ServiceOffer, baseURL string, explicit *schemas.StorefrontProfile) string {
	baseURL = strings.TrimRight(baseURL, "/")
	profile := storefront.ResolvePublished(explicit, baseURL)

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
	// Higher listing weight sorts earlier; equal weights fall back to name.
	// Category grouping is applied client-side on the storefront.
	sort.Slice(ready, func(i, j int) bool {
		wi, wj := ready[i].Spec.Listing.Weight, ready[j].Spec.Listing.Weight
		if wi != wj {
			return wi > wj
		}
		return ready[i].Name < ready[j].Name
	})

	services := make([]schemas.ServiceCatalogEntry, 0, len(ready))
	for _, offer := range ready {
		desc := offer.Spec.Registration.Description
		if desc == "" {
			desc = fmt.Sprintf("x402 payment-gated %s service", fallbackOfferType(offer))
		}
		modelName := catalogModelName(offer)

		drainEndsAt := ""
		if offer.IsDraining() {
			drainEndsAt = offer.DrainEndsAt().UTC().Format(time.RFC3339)
		}

		// Skills source matches the 402 renderer: for type=agent the
		// resolved Agent allow-list wins (controller-populated), with a
		// fallback to spec.registration.skills for non-agent offers
		// that still want to surface skill tags on discovery.
		var skills []string
		if offer.IsAgent() && offer.Status.AgentResolution != nil && len(offer.Status.AgentResolution.Skills) > 0 {
			skills = append([]string(nil), offer.Status.AgentResolution.Skills...)
		} else if len(offer.Spec.Registration.Skills) > 0 {
			skills = append([]string(nil), offer.Spec.Registration.Skills...)
		}

		// Hostname-bound offers advertise their dedicated origin (the
		// public path-world is rooted at "/") — the shared-origin path
		// keeps working as an alias but is no longer what buyers are
		// taught.
		endpoint := baseURL + offer.EffectivePath()
		if origin := offer.EffectiveOrigin(); origin != "" {
			endpoint = origin
		}
		svc := schemas.ServiceCatalogEntry{
			Name:                offer.Name,
			Namespace:           offer.Namespace,
			Type:                fallbackOfferType(offer),
			Model:               modelName,
			Endpoint:            endpoint,
			Price:               describeOfferPrice(offer),
			PayTo:               offer.Spec.Payment.PayTo,
			Network:             offer.Spec.Payment.Network,
			Description:         desc,
			DescriptionHTML:     string(storefront.RenderRichText(desc)),
			Skills:              skills,
			Category:            offerCategory(offer),
			Weight:              offer.Spec.Listing.Weight,
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
			if raw != "" && catalogAssetHasKnownDecimals(asset) {
				svc.PriceAtomicUnits = decimalToAtomicString(raw, int(asset.Decimals))
			}
		}

		// Full multi-currency view (always >= 1 entry; payments[0] mirrors the
		// flat fields above). The storefront renders one pay-row per option.
		svc.Payments = buildCatalogPayments(offer)

		// Canonical buyer instructions — generated once here and rendered
		// verbatim by the storefront (and any other consumer) so how-to-buy
		// copy cannot drift between surfaces. The 402 paywall page builds
		// its prompt cards from the same buyprompts package.
		buyURL := svc.Endpoint
		if rt, ok := primaryPaidRoute(offer); ok {
			// Route-table offers: teach buyers the primary paid route, not
			// the offer root. svc.Endpoint stays the base (public contract).
			buyURL = svc.Endpoint + openAPIRelPathForRoute(rt.Path)
		}
		buy := buyprompts.Build(buyprompts.Input{
			Type:         fallbackOfferType(offer),
			URL:          buyURL,
			SiteURL:      baseURL,
			Model:        modelName,
			PriceDisplay: svc.Price,
			NetworkLabel: offer.Spec.Payment.Network,
		})
		svc.Buy = &buy
		svc.OpenAPIPath = openAPIPrimaryPathForOffer(offer)
		svc.DocsPath = openAPIDocsAnchorForOffer(offer)

		services = append(services, svc)
	}

	theme := storefront.ResolveTheme(profile.Theme, profile.AccentColor)
	catalog := schemas.ServiceCatalog{
		SchemaVersion:   schemas.ServiceCatalogSchemaVersion,
		DisplayName:     profile.DisplayName,
		Tagline:         profile.Tagline,
		LogoURL:         profile.LogoURL,
		Services:        services,
		Theme:           theme.Name,
		ThemeVars:       theme.Vars,
		FaviconURL:      profile.FaviconURL,
		OGImageURL:      profile.OGImageURL,
		Description:     profile.Description,
		DescriptionHTML: string(storefront.RenderRichText(profile.Description)),
		CustomCSS:       storefront.SafeCustomCSS(profile.CustomCSS),
	}
	if catalog.Services == nil {
		catalog.Services = []schemas.ServiceCatalogEntry{}
	}

	out, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return fallbackServiceCatalogJSON(baseURL)
	}
	return string(out)
}

func fallbackServiceCatalogJSON(baseURL string) string {
	profile := storefront.ResolvePublished(nil, baseURL)
	theme := storefront.ResolveTheme(profile.Theme, profile.AccentColor)
	catalog := schemas.ServiceCatalog{
		SchemaVersion: schemas.ServiceCatalogSchemaVersion,
		DisplayName:   profile.DisplayName,
		Tagline:       profile.Tagline,
		LogoURL:       profile.LogoURL,
		Services:      []schemas.ServiceCatalogEntry{},
		Theme:         theme.Name,
		ThemeVars:     theme.Vars,
	}
	out, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return `{"schemaVersion":"1","displayName":"Obol Stack","tagline":"Unlock Agent and API services with digital payments.","logoUrl":"/obol-stack-logo.png","services":[]}`
	}
	return string(out)
}

// offerPriceRawAndUnit returns the raw decimal price string and slot for the
// offer's PRIMARY payment. Per-payment callers use paymentPriceRawAndUnit.
func offerPriceRawAndUnit(offer *monetizeapi.ServiceOffer) (string, string) {
	return paymentPriceRawAndUnit(offer.Spec.Payment)
}

// paymentPriceRawAndUnit returns the raw decimal price string and which slot it
// occupies for a single payment option. Only one of perRequest / perMTok /
// perHour / perEpoch is expected to be set.
func paymentPriceRawAndUnit(p monetizeapi.ServiceOfferPayment) (string, string) {
	switch {
	case p.Price.PerRequest != "":
		return p.Price.PerRequest, "perRequest"
	case p.Price.PerMTok != "":
		return p.Price.PerMTok, "perMTok"
	case p.Price.PerHour != "":
		return p.Price.PerHour, "perHour"
	default:
		return "", ""
	}
}

// offerAssetJSON resolves the settlement asset block for the offer's PRIMARY
// payment. Per-payment callers use paymentAssetJSON.
func offerAssetJSON(offer *monetizeapi.ServiceOffer) *schemas.ServiceCatalogAsset {
	return paymentAssetJSON(offer.Spec.Payment)
}

// paymentAssetJSON resolves the settlement asset block for a single payment
// option. If the option carries an explicit asset it is used verbatim; if only
// the network is set, defaults for USDC on that chain are filled in (matching
// the verifier's behavior when the seller did not pass --token).
func paymentAssetJSON(p monetizeapi.ServiceOfferPayment) *schemas.ServiceCatalogAsset {
	a := p.Asset
	if a.Address == "" && a.Symbol == "" && a.EIP712Name == "" {
		// No explicit asset — fall back to the chain's default USDC entry.
		if def, ok := defaultUSDCForNetwork(p.Network); ok {
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
	if def, ok := defaultUSDCForNetwork(p.Network); ok && shouldBackfillDefaultAsset(out, def) {
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

func shouldBackfillDefaultAsset(out *schemas.ServiceCatalogAsset, def schemas.ServiceCatalogAsset) bool {
	if out == nil {
		return false
	}
	addressMatchesDefault := out.Address == "" || strings.EqualFold(out.Address, def.Address)
	symbolMatchesDefault := out.Symbol == "" || strings.EqualFold(out.Symbol, def.Symbol)
	return addressMatchesDefault && symbolMatchesDefault
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
	return describePaymentPrice(offer.Spec.Payment)
}

// describeOfferPaymentsInline renders every accepted payment option of an
// offer for the compact catalog table, e.g.
// "1 USDC/request on base · 10 OBOL/request on ethereum". Buyers satisfy
// any one of the listed options.
func describeOfferPaymentsInline(offer *monetizeapi.ServiceOffer) string {
	payments := offer.EffectivePayments()
	parts := make([]string, 0, len(payments))
	for i := range payments {
		parts = append(parts, describePaymentInline(payments[i]))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " · ")
}

// describePaymentInline is one option in compact form: "<price> on <network>".
func describePaymentInline(p monetizeapi.ServiceOfferPayment) string {
	return describePaymentPrice(p) + " on " + firstNonEmpty(p.Network, "—")
}

// describePaymentDetail is one option fully expanded for the per-service
// detail block, e.g.
// "1 USDC per request on `base` (eip155:8453) — pay to `0x…`; token `0x833…` (USDC, 6 decimals, eip3009)".
func describePaymentDetail(p monetizeapi.ServiceOfferPayment) string {
	var b strings.Builder
	// describePaymentPrice yields "1 USDC/request"; spell the unit out for prose.
	b.WriteString(strings.Replace(describePaymentPrice(p), "/", " per ", 1))
	b.WriteString(" on `")
	b.WriteString(firstNonEmpty(p.Network, "—"))
	b.WriteString("`")
	if caip, _ := caip2ForNetwork(p.Network); caip != "" {
		b.WriteString(" (" + caip + ")")
	}
	if p.PayTo != "" {
		b.WriteString(" — pay to `" + p.PayTo + "`")
	}
	if a := paymentAssetJSON(p); a != nil && (a.Address != "" || a.Symbol != "") {
		b.WriteString("; token")
		if a.Address != "" {
			b.WriteString(" `" + a.Address + "`")
		}
		meta := make([]string, 0, 3)
		if a.Symbol != "" {
			meta = append(meta, a.Symbol)
		}
		if a.Decimals > 0 {
			meta = append(meta, fmt.Sprintf("%d decimals", a.Decimals))
		}
		if a.TransferMethod != "" {
			meta = append(meta, a.TransferMethod)
		}
		if len(meta) > 0 {
			b.WriteString(" (" + strings.Join(meta, ", ") + ")")
		}
	}
	return b.String()
}

// skillCatalogRouteLines renders the per-route list for offers with a
// declared route table (spec.routes). One line per route: methods, full
// URL, gate/price, and the route summary. Offers without a route table
// contribute nothing — their single implicit catch-all is already fully
// described by the Endpoint/Payment lines above.
func skillCatalogRouteLines(offer *monetizeapi.ServiceOffer, endpoint string) []string {
	if len(offer.Spec.Routes) == 0 {
		return nil
	}
	lines := []string{"- **Routes** (per-route gating; paths outside this table are not served):"}
	for _, rt := range offer.EffectiveRoutes() {
		gate := rt.EffectiveGate()
		methods := strings.Join(rt.Methods, "|")
		if methods == "" {
			if gate == monetizeapi.GatePaid {
				methods = "POST"
			} else {
				methods = "GET"
			}
		}
		var cost string
		switch {
		case gate == monetizeapi.GateFree:
			cost = "free"
		case gate == monetizeapi.GateAuth:
			cost = "free, wallet sign-in required (SIWX/EIP-4361 — see the offer's `/auth` page)"
		case rt.HasPriceOverride():
			p := offer.EffectivePayments()[0]
			p.Price = rt.Price
			cost = describePaymentPrice(p)
		default:
			cost = describeOfferPrice(offer)
		}
		line := fmt.Sprintf("  - `%s %s%s` — %s", methods, endpoint, strings.TrimSuffix(rt.Path, "/*"), cost)
		if strings.HasSuffix(rt.Path, "/*") {
			line += " (covers sub-paths)"
		}
		if rt.Summary != "" {
			line += " — " + rt.Summary
		}
		lines = append(lines, line)
	}
	return lines
}

// offerCallHint returns a one-line "how to invoke" hint for the service
// detail block, derived from the offer type. inference/agent both speak the
// OpenAI chat-completions wire format; http is operator-defined.
func offerCallHint(offer *monetizeapi.ServiceOffer, endpoint string) string {
	switch {
	case offer.IsInference(), offer.IsAgent():
		return fmt.Sprintf("`POST %s/v1/chat/completions` — OpenAI-compatible chat completions (supports `stream: true`)", endpoint)
	case strings.EqualFold(offer.Spec.Type, "fine-tuning"):
		return fmt.Sprintf("`POST %s` — multipart fine-tuning job (operator-defined payload)", endpoint)
	default:
		return fmt.Sprintf("`%s` — operator-defined request shape; see `/openapi.json`", endpoint)
	}
}

// describePaymentPrice renders a single payment option as "<price> <SYMBOL>/<unit>".
func describePaymentPrice(p monetizeapi.ServiceOfferPayment) string {
	// Source the symbol from (in order): explicit asset metadata on the
	// option, the resolved chain-default settlement asset, hard-coded "USDC"
	// only as the last-resort fallback for unknown chains. Mislabeling
	// OBOL-priced services as "USDC" on the discovery surfaces (storefront /
	// skill.md) caused buyers to queue up the wrong asset on rc7-rc9.
	symbol := p.Asset.Symbol
	if symbol == "" {
		if a := paymentAssetJSON(p); a != nil && a.Symbol != "" {
			symbol = a.Symbol
		}
	}
	if symbol == "" {
		symbol = "USDC"
	}
	switch {
	case p.Price.PerRequest != "":
		return p.Price.PerRequest + " " + symbol + "/request"
	case p.Price.PerMTok != "":
		return p.Price.PerMTok + " " + symbol + "/MTok"
	case p.Price.PerHour != "":
		return p.Price.PerHour + " " + symbol + "/hour"
	default:
		return "—"
	}
}

// buildCatalogPayments renders every accepted payment option of an offer into
// catalog payment entries (one per currency/network). payments[0] is the
// primary and mirrors the entry's flat fields.
func buildCatalogPayments(offer *monetizeapi.ServiceOffer) []schemas.ServiceCatalogPaymentOption {
	payments := offer.EffectivePayments()
	out := make([]schemas.ServiceCatalogPaymentOption, 0, len(payments))
	for i := range payments {
		p := payments[i]
		opt := schemas.ServiceCatalogPaymentOption{
			Price:   describePaymentPrice(p),
			PayTo:   p.PayTo,
			Network: p.Network,
		}
		opt.PriceRaw, opt.PriceUnit = paymentPriceRawAndUnit(p)
		opt.CAIP2Network, opt.ChainID = caip2ForNetwork(p.Network)
		if asset := paymentAssetJSON(p); asset != nil {
			opt.Asset = asset
			if opt.PriceRaw != "" && catalogAssetHasKnownDecimals(asset) {
				opt.PriceAtomicUnits = decimalToAtomicString(opt.PriceRaw, int(asset.Decimals))
			}
		}
		out = append(out, opt)
	}
	return out
}

func catalogAssetHasKnownDecimals(asset *schemas.ServiceCatalogAsset) bool {
	if asset == nil {
		return false
	}
	return asset.Decimals > 0 || asset.EIP712Domain != nil
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
