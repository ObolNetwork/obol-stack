package serviceoffercontroller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
)

var upstreamOpenAPIClient = &http.Client{Timeout: 3 * time.Second}

// tryUpstreamOpenAPI is the seam tests replace.
var tryUpstreamOpenAPI = fetchUpstreamOpenAPI

func fetchUpstreamOpenAPI(offer *monetizeapi.ServiceOffer) map[string]any {
	if offer == nil || offer.IsAgent() || offer.IsInference() {
		return nil
	}
	if strings.TrimSpace(offer.Spec.Upstream.Service) == "" {
		return nil
	}
	base := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
		offer.Spec.Upstream.Service,
		offer.EffectiveNamespace(),
		offer.EffectivePort(),
	)
	for _, path := range upstreamOpenAPIPathCandidates(offer) {
		doc, err := getJSONMap(base + path)
		if err != nil || doc == nil {
			continue
		}
		paths, _ := doc["paths"].(map[string]any)
		if len(paths) == 0 {
			continue
		}
		return doc
	}
	return nil
}

func upstreamOpenAPIPathCandidates(offer *monetizeapi.ServiceOffer) []string {
	var out []string
	if offer.Spec.Registration.Metadata != nil {
		if p := strings.TrimSpace(offer.Spec.Registration.Metadata["openapiPath"]); p != "" {
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			out = append(out, p)
		}
	}
	out = append(out, "/v1/openapi.json", "/openapi.json")
	return out
}

func getJSONMap(url string) (map[string]any, error) {
	resp, err := upstreamOpenAPIClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func rewriteUpstreamOpenAPI(doc map[string]any, offer *monetizeapi.ServiceOffer, profile schemas.StorefrontProfile) (string, bool) {
	if doc == nil {
		return "", false
	}
	out := map[string]any{}
	for k, v := range doc {
		out[k] = v
	}
	origin := offer.EffectiveOrigin()
	out["servers"] = []any{map[string]any{"url": origin}}
	info, _ := out["info"].(map[string]any)
	if info == nil {
		info = map[string]any{}
	} else {
		copied := map[string]any{}
		for k, v := range info {
			copied[k] = v
		}
		info = copied
	}
	if title := strings.TrimSpace(offer.Spec.Registration.Name); title != "" {
		info["title"] = title
	}
	if desc := strings.TrimSpace(offer.Spec.Registration.Description); desc != "" {
		info["description"] = desc
	}
	if email := strings.TrimSpace(profile.ContactEmail); email != "" {
		info["contact"] = map[string]any{"name": strings.TrimSpace(profile.DisplayName), "email": email}
	}
	out["info"] = info
	if _, has := out["x-discovery"]; !has {
		out["x-discovery"] = map[string]any{
			"source": "upstream-openapi",
			"note":   "Full first-party API surface; paid ops carry x-payment-info + 402 responses (x402scan OpenAPI-first).",
		}
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func buildOfferWellKnownX402FromOpenAPI(offer *monetizeapi.ServiceOffer, doc map[string]any) string {
	if doc == nil {
		return ""
	}
	origin := strings.TrimRight(offer.EffectiveOrigin(), "/")
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		return ""
	}
	pathKeys := make([]string, 0, len(paths))
	for p := range paths {
		pathKeys = append(pathKeys, p)
	}
	sort.Strings(pathKeys)
	defaultAccepts := []any{}
	for _, p := range offer.EffectivePayments() {
		defaultAccepts = append(defaultAccepts, wellKnownAccept(p))
	}
	if len(defaultAccepts) == 0 {
		return ""
	}
	var resources []any
	for _, p := range pathKeys {
		item, _ := paths[p].(map[string]any)
		if item == nil {
			continue
		}
		for _, m := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
			op, _ := item[m].(map[string]any)
			if op == nil || !openAPIOpIsPaid(op) {
				continue
			}
			desc, _ := op["summary"].(string)
			if desc == "" {
				desc, _ = op["description"].(string)
			}
			if desc == "" {
				desc = offerDescription(offer, "x402 payment-gated service.")
			}
			resources = append(resources, map[string]any{
				"resource":    origin + joinOpenAPIPath("/", p),
				"type":        "http",
				"method":      strings.ToUpper(m),
				"description": desc,
				"x402Version": 2,
				"accepts":     defaultAccepts,
			})
		}
	}
	if len(resources) == 0 {
		return ""
	}
	if len(resources) > 200 {
		resources = resources[:200]
	}
	encoded, err := json.MarshalIndent(map[string]any{"x402Version": 2, "resources": resources}, "", "  ")
	if err != nil {
		return ""
	}
	return string(encoded)
}

func openAPIOpIsPaid(op map[string]any) bool {
	if op == nil {
		return false
	}
	if gate, _ := op["x-gate"].(string); gate == "free" {
		return false
	}
	if _, hasPay := op["x-payment-info"]; hasPay {
		return true
	}
	if sec, ok := op["security"].([]any); ok && len(sec) == 0 {
		return false
	}
	if responses, ok := op["responses"].(map[string]any); ok {
		if _, has402 := responses["402"]; has402 {
			if sec, ok := op["security"].([]any); ok && len(sec) == 0 {
				return false
			}
			return true
		}
	}
	return false
}

func buildOfferAgentRegistration(offer *monetizeapi.ServiceOffer, profile schemas.StorefrontProfile) string {
	origin := strings.TrimRight(offer.EffectiveOrigin(), "/")
	name := strings.TrimSpace(offer.Spec.Registration.Name)
	if name == "" {
		name = offer.Name
	}
	desc := strings.TrimSpace(offer.Spec.Registration.Description)
	if desc == "" {
		desc = offerDescription(offer, "x402 payment-gated service.")
	}
	image := strings.TrimSpace(offer.Spec.Registration.Image)
	if image == "" {
		if logo := strings.TrimSpace(profile.LogoURL); logo != "" && (strings.HasPrefix(logo, "http://") || strings.HasPrefix(logo, "https://")) {
			image = logo
		} else {
			image = origin + "/agent-icon.png"
		}
	}
	services := []erc8004.ServiceDef{{Name: "web", Endpoint: origin, Version: "1.0.0"}}
	if len(offer.Spec.Registration.Services) > 0 {
		var scoped []erc8004.ServiceDef
		for _, s := range offer.Spec.Registration.Services {
			ep := strings.TrimSpace(s.Endpoint)
			if ep == "" {
				continue
			}
			if strings.Contains(ep, "://") && !strings.HasPrefix(ep, origin) {
				continue
			}
			scoped = append(scoped, erc8004.ServiceDef{
				Name:     defaultString(s.Name, "web"),
				Endpoint: ep,
				Version:  defaultString(s.Version, "1.0.0"),
			})
		}
		if len(scoped) > 0 {
			services = scoped
		}
	}
	if len(offer.Spec.Registration.Skills) > 0 || len(offer.Spec.Registration.Domains) > 0 {
		services = append(services, erc8004.ServiceDef{
			Name: "OASF", Version: "0.8",
			Skills: offer.Spec.Registration.Skills, Domains: offer.Spec.Registration.Domains,
		})
	}
	doc := erc8004.AgentRegistration{
		Type: erc8004.RegistrationType, Name: name, Description: desc, Image: image,
		Active: offer.Spec.Registration.Enabled, X402Support: true, Services: services,
		SupportedTrust: offer.Spec.Registration.SupportedTrust,
	}
	if meta := nonEmptyStringMap(offer.Spec.Registration.Metadata); len(meta) > 0 {
		doc.Metadata = meta
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return `{"type":"` + erc8004.RegistrationType + `","name":"` + name + `","active":false,"services":[]}`
	}
	return string(encoded)
}
