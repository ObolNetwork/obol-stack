package serviceoffercontroller

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// skillBundlePort is the fixed port the controller-rendered skill bundle
// server listens on. The CLI pins spec.upstream.port to this value and
// reconcileSkillBundle rejects anything else (anti-spoof guard — see the
// InvalidSkillUpstream branch).
const skillBundlePort = int64(8080)

// skillBundleHTTPDConf maps the two file extensions the bundle server
// serves to their MIME types (busybox httpd /etc/httpd.conf format).
const skillBundleHTTPDConf = ".tar.gz:application/gzip\n.json:application/json\n"

// skillBundleMetaName returns the name of the controller-rendered metadata
// ConfigMap (skill.json + httpd.conf) that sits next to the operator's
// bundle ConfigMap. Equals SkillBundleWorkloadName(offerName)+"-meta" for
// every name that fits the 253-char DNS-subdomain limit; pathological
// names go through the shared safeName truncate+hash fallback instead of
// blindly appending past the limit.
func skillBundleMetaName(offerName string) string {
	return safeName("so-", offerName, "-bundle-meta")
}

// skillBundleLabels is the shared label set for the bundle server children
// (Deployment selector/template, Service selector, meta ConfigMap). Same
// shape as agentIdentityLabels / the skill catalog labels.
func skillBundleLabels(offer *monetizeapi.ServiceOffer) map[string]any {
	return map[string]any{
		"app":                 monetizeapi.SkillBundleWorkloadName(offer.Name),
		"obol.org/managed-by": "serviceoffer-controller",
	}
}

// skillBundleDocument is the machine-readable descriptor served at
// /skill.json next to the artifact. It doubles as the upstream health
// check target (the CLI pins spec.upstream.healthPath to /skill.json), so
// UpstreamHealthy only goes True once the bundle server actually serves
// the descriptor for the validated bundle.
type skillBundleDocument struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	SHA256      string `json:"sha256"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	Offer       string `json:"offer"`
	Namespace   string `json:"namespace"`
}

func buildSkillBundleJSON(offer *monetizeapi.ServiceOffer) (string, error) {
	document := skillBundleDocument{
		Name:        offer.Spec.Skill.Name,
		Version:     offer.Spec.Skill.Version,
		SHA256:      strings.ToLower(offer.Spec.Skill.SHA256),
		DisplayName: offer.Spec.Skill.DisplayName,
		Description: offer.Spec.Skill.Description,
		Offer:       offer.Name,
		Namespace:   offer.Namespace,
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal skill.json for %s/%s: %w", offer.Namespace, offer.Name, err)
	}
	return string(data), nil
}

// buildSkillBundleMetaConfigMap renders the controller-owned metadata
// ConfigMap mounted into the bundle server: skill.json (descriptor +
// health target) and httpd.conf (MIME map). Owner-referenced to the offer
// so GC removes it when the offer is deleted.
func buildSkillBundleMetaConfigMap(offer *monetizeapi.ServiceOffer) (*unstructured.Unstructured, error) {
	skillJSON, err := buildSkillBundleJSON(offer)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":            skillBundleMetaName(offer.Name),
				"namespace":       offer.Namespace,
				"ownerReferences": []any{ownerRefMap(offer)},
				"labels":          skillBundleLabels(offer),
			},
			"data": map[string]any{
				"skill.json": skillJSON,
				"httpd.conf": skillBundleHTTPDConf,
			},
		},
	}, nil
}

// buildSkillBundleDeployment renders the static bundle server: a busybox
// httpd serving /www/bundle.tar.gz (projected from the operator's bundle
// ConfigMap) and /www/skill.json (projected from the meta ConfigMap).
// Restricted-PSS securityContext copied from the skill catalog /
// agentidentity httpd pattern — the same admission profile applies to any
// namespace that enforces Restricted PSS, and there is no reason for a
// static file server to run with more privilege.
//
// The pod template carries obol.org/content-hash = spec.skill.sha256[:8]
// so re-publishing a bundle (new hash) rolls the pod even though the
// Deployment spec is otherwise unchanged.
func buildSkillBundleDeployment(offer *monetizeapi.ServiceOffer) *unstructured.Unstructured {
	name := monetizeapi.SkillBundleWorkloadName(offer.Name)
	labels := skillBundleLabels(offer)
	contentHash := strings.ToLower(offer.Spec.Skill.SHA256)
	if len(contentHash) > 8 {
		contentHash = contentHash[:8]
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       offer.Namespace,
				"ownerReferences": []any{ownerRefMap(offer)},
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
									map[string]any{"containerPort": skillBundlePort, "protocol": "TCP"},
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
							// Single projected volume so both ConfigMaps land in
							// the same /www docroot (two configMap volumes cannot
							// share a mountPath).
							map[string]any{
								"name": "content",
								"projected": map[string]any{
									"sources": []any{
										map[string]any{
											"configMap": map[string]any{
												"name":  offer.Spec.Skill.BundleConfigMap,
												"items": []any{map[string]any{"key": monetizeapi.SkillBundleKey, "path": monetizeapi.SkillBundleKey}},
											},
										},
										map[string]any{
											"configMap": map[string]any{
												"name":  skillBundleMetaName(offer.Name),
												"items": []any{map[string]any{"key": "skill.json", "path": "skill.json"}},
											},
										},
									},
								},
							},
							map[string]any{
								"name": "httpdconf",
								"configMap": map[string]any{
									"name":  skillBundleMetaName(offer.Name),
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

// buildSkillBundleService renders the ClusterIP Service in front of the
// bundle server. Its name is the deterministic upstream the CLI pins into
// spec.upstream.service, which is how the existing reconcileUpstream and
// routeRuleFromOffer paths work unchanged for type=skill offers.
func buildSkillBundleService(offer *monetizeapi.ServiceOffer) *unstructured.Unstructured {
	name := monetizeapi.SkillBundleWorkloadName(offer.Name)
	labels := skillBundleLabels(offer)
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       offer.Namespace,
				"ownerReferences": []any{ownerRefMap(offer)},
				"labels":          labels,
			},
			"spec": map[string]any{
				"type":     "ClusterIP",
				"selector": labels,
				"ports": []any{
					map[string]any{"port": skillBundlePort, "targetPort": skillBundlePort, "protocol": "TCP"},
				},
			},
		},
	}
}
