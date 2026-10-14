package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// liteLLMModelRuntime is spec.model.runtime for a seller-pinned LiteLLM model.
const liteLLMModelRuntime = "litellm"

// isLiteLLMUpstreamName reports whether an `obol sell http --upstream` value
// targets the cluster LiteLLM gateway (the upstream the verifier injects
// the master key on, and pins the model for).
func isLiteLLMUpstreamName(upstream string) bool {
	return strings.EqualFold(strings.TrimSpace(upstream), "litellm")
}

// checkSellHTTPModel validates --model for `obol sell http`. On a LiteLLM
// upstream the seller must pin the model (the gateway overwrites the buyer's
// `model`); everywhere else the flag has no effect, so it is rejected rather
// than silently stored. configured/listErr are the LiteLLM model list (or
// the error reading it); a list error only skips the existence check.
func checkSellHTTPModel(upstream, modelName string, configured []string, listErr error) (string, error) {
	modelName = strings.TrimSpace(modelName)
	if !isLiteLLMUpstreamName(upstream) {
		if modelName != "" {
			return "", errors.New("--model only applies to --upstream litellm, where the seller gateway pins it; other upstreams serve whatever model they run")
		}
		return "", nil
	}
	if modelName == "" {
		return "", errors.New("--upstream litellm requires --model <litellm model_name>: buyers can't choose the model, the offer serves exactly the one you pin (see `obol model list`)")
	}
	return modelName, checkLiteLLMModelName(modelName, configured, listErr)
}

// checkLiteLLMModelName rejects names that aren't a single concrete LiteLLM
// model: wildcards and comma lists (LiteLLM fans a comma list out to every
// named model) and, when the list could be read, names LiteLLM doesn't serve.
func checkLiteLLMModelName(modelName string, configured []string, listErr error) error {
	if strings.ContainsAny(modelName, "*,") {
		return fmt.Errorf("--model %q must be one concrete LiteLLM model, not a wildcard or list", modelName)
	}
	if listErr == nil && len(configured) > 0 && !isModelConfigured(modelName, configured) {
		return fmt.Errorf("model %q is not configured in LiteLLM\n  Available: %s\n  Run `obol model list` to see models",
			modelName, strings.Join(configured, ", "))
	}
	return nil
}

// liteLLMConfiguredModelsFn reads LiteLLM's configured model names;
// overridable in tests.
var liteLLMConfiguredModelsFn = model.GetConfiguredModels

// configuredLiteLLMModels reads LiteLLM's model list for --model validation,
// noting (not failing) when it can't be read.
func configuredLiteLLMModels(cfg *config.Config, u *ui.UI) ([]string, error) {
	configured, err := model.GetConfiguredModels(cfg)
	if err != nil {
		u.Dim(fmt.Sprintf("Could not verify --model against LiteLLM (%v); continuing", err))
	}
	return configured, err
}

// liteLLMModelSpec is the spec.model block for a seller-pinned LiteLLM model.
func liteLLMModelSpec(modelName string) map[string]any {
	return map[string]any{"name": modelName, "runtime": liteLLMModelRuntime}
}

// checkFromJSONLiteLLMModel applies the --model requirement to a raw
// `sell http --from-json` spec: a non-agent, non-inference offer on the
// LiteLLM upstream must carry spec.model.name.
func checkFromJSONLiteLLMModel(spec map[string]any) error {
	upstream, _ := spec["upstream"].(map[string]any)
	svc, _ := upstream["service"].(string)
	typ, _ := spec["type"].(string)
	if !isLiteLLMUpstreamName(svc) || typ == "agent" || typ == "" || typ == "inference" {
		return nil
	}
	m, _ := spec["model"].(map[string]any)
	if name, _ := m["name"].(string); strings.TrimSpace(name) == "" {
		return errors.New(`upstream "litellm" requires spec.model.name (and spec.model.runtime: litellm): the seller pins the model buyers are served`)
	}
	return nil
}

// sellUpdateModelPatch returns the spec.model merge-patch value for
// `obol sell update --model`, refusing offers whose model the gateway
// doesn't pin (only LiteLLM-backed http offers take a seller-chosen model).
func sellUpdateModelPatch(offer *monetizeapi.ServiceOffer, modelName string, configured []string, listErr error) (map[string]any, error) {
	modelName = strings.TrimSpace(modelName)
	if offer.IsAgent() || offer.IsInference() || !isLiteLLMUpstreamName(offer.Spec.Upstream.Service) {
		return nil, fmt.Errorf("--model only applies to offers on the litellm upstream (this one is type %q on %q); agent offers follow their Agent's model and inference offers their gateway",
			offer.Spec.Type, offer.Spec.Upstream.Service)
	}
	if modelName == "" {
		return nil, errors.New("--model must not be empty")
	}
	if err := checkLiteLLMModelName(modelName, configured, listErr); err != nil {
		return nil, err
	}
	return liteLLMModelSpec(modelName), nil
}
