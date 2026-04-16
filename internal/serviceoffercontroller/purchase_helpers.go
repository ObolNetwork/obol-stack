package serviceoffercontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"gopkg.in/yaml.v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	buyerConfigCM    = "x402-buyer-config"
	buyerAuthsCM     = "x402-buyer-auths"
	litellmSecret    = "litellm-secrets"
	litellmMasterKey = "LITELLM_MASTER_KEY"
)

// litellmBaseURL returns the LiteLLM HTTP base URL. In production it resolves
// to the in-cluster Service DNS; tests can set Controller.litellmURLOverride
// to an httptest server instead.
func (c *Controller) litellmBaseURL(ns string) string {
	if c.litellmURLOverride != "" {
		return c.litellmURLOverride
	}
	return fmt.Sprintf("http://litellm.%s.svc:4000", ns)
}

// getLiteLLMMasterKey reads the master key from the litellm-secrets Secret.
// The controller needs `secrets:get` RBAC on this Secret in the target
// namespace (granted to the serviceoffer-controller ClusterRole).
func (c *Controller) getLiteLLMMasterKey(ctx context.Context, ns string) (string, error) {
	secret, err := c.kubeClient.CoreV1().Secrets(ns).Get(ctx, litellmSecret, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get %s/%s: %w", ns, litellmSecret, err)
	}
	key := string(secret.Data[litellmMasterKey])
	if key == "" {
		return "", fmt.Errorf("%s has empty %s", litellmSecret, litellmMasterKey)
	}
	return key, nil
}

// hotAddLiteLLMModel adds a model via the LiteLLM /model/new HTTP API. The
// in-memory router is updated without a pod restart, preserving the x402-buyer
// sidecar's consumed-auth state (which lives in a pod-local emptyDir and would
// be wiped by a rollout).
//
// Returns an error if the API call fails; callers fall back to a pod restart
// only as a last resort — see addLiteLLMModelEntry.
func (c *Controller) hotAddLiteLLMModel(ctx context.Context, ns string, entry model.ModelEntry) error {
	masterKey, err := c.getLiteLLMMasterKey(ctx, ns)
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]any{
		"model_name": entry.ModelName,
		"litellm_params": map[string]any{
			"model":    entry.LiteLLMParams.Model,
			"api_base": entry.LiteLLMParams.APIBase,
			"api_key":  entry.LiteLLMParams.APIKey,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal model_new body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.litellmBaseURL(ns)+"/model/new", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+masterKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /model/new: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("POST /model/new: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// hotDeleteLiteLLMModel removes a model via the LiteLLM /model/info →
// /model/delete API. It first queries model IDs by name, then deletes each
// matching entry. No pod restart — the router mutates in place.
func (c *Controller) hotDeleteLiteLLMModel(ctx context.Context, ns, modelName string) error {
	masterKey, err := c.getLiteLLMMasterKey(ctx, ns)
	if err != nil {
		return err
	}

	ids, err := c.litellmModelIDsByName(ctx, ns, masterKey, modelName)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	var firstErr error
	for _, id := range ids {
		body, _ := json.Marshal(map[string]string{"id": id})
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost,
			c.litellmBaseURL(ns)+"/model/delete", bytes.NewReader(body))
		if reqErr != nil {
			if firstErr == nil {
				firstErr = reqErr
			}
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+masterKey)

		resp, doErr := c.httpClient.Do(req)
		if doErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("POST /model/delete: %w", doErr)
			}
			continue
		}
		if resp.StatusCode >= 300 {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			if firstErr == nil {
				firstErr = fmt.Errorf("POST /model/delete: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
			}
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	return firstErr
}

// litellmModelIDsByName queries /model/info and returns the model_id values
// for every entry whose model_name matches. LiteLLM stores one entry per
// deployment; a single name can map to multiple IDs under load-balanced routes.
func (c *Controller) litellmModelIDsByName(ctx context.Context, ns, masterKey, modelName string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.litellmBaseURL(ns)+"/model/info", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+masterKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET /model/info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET /model/info: %s", resp.Status)
	}

	var payload struct {
		Data []struct {
			ModelName string `json:"model_name"`
			ModelInfo struct {
				ID string `json:"id"`
			} `json:"model_info"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode model_info: %w", err)
	}

	var ids []string
	for _, entry := range payload.Data {
		if entry.ModelName == modelName && entry.ModelInfo.ID != "" {
			ids = append(ids, entry.ModelInfo.ID)
		}
	}
	return ids, nil
}

// ── ConfigMap merge (optimistic concurrency) ────────────────────────────────

func (c *Controller) mergeBuyerConfig(ctx context.Context, ns, name string, upstream map[string]any) error {
	cm, err := c.kubeClient.CoreV1().ConfigMaps(ns).Get(ctx, buyerConfigCM, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get %s/%s: %w", ns, buyerConfigCM, err)
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	delete(cm.Data, "config.json")
	configJSON, _ := json.MarshalIndent(upstream, "", "  ")
	cm.Data[name+".json"] = string(configJSON)

	_, err = c.kubeClient.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

func (c *Controller) mergeBuyerAuths(ctx context.Context, ns, name string, auths []map[string]string) error {
	cm, err := c.kubeClient.CoreV1().ConfigMaps(ns).Get(ctx, buyerAuthsCM, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get %s/%s: %w", ns, buyerAuthsCM, err)
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	delete(cm.Data, "auths.json")
	authsJSON, _ := json.MarshalIndent(auths, "", "  ")
	cm.Data[name+".json"] = string(authsJSON)

	_, err = c.kubeClient.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

func (c *Controller) removeBuyerUpstream(ctx context.Context, ns, name string) {
	// Remove from config.
	cm, err := c.kubeClient.CoreV1().ConfigMaps(ns).Get(ctx, buyerConfigCM, metav1.GetOptions{})
	if err == nil {
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		delete(cm.Data, "config.json")
		delete(cm.Data, name+".json")
		c.kubeClient.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
	}

	// Remove from auths.
	authsCM, err := c.kubeClient.CoreV1().ConfigMaps(ns).Get(ctx, buyerAuthsCM, metav1.GetOptions{})
	if err == nil {
		if authsCM.Data == nil {
			authsCM.Data = make(map[string]string)
		}
		delete(authsCM.Data, "auths.json")
		delete(authsCM.Data, name+".json")
		c.kubeClient.CoreV1().ConfigMaps(ns).Update(ctx, authsCM, metav1.UpdateOptions{})
	}
}

// addLiteLLMModelEntry adds a paid/<model> route to LiteLLM. Writes the
// ConfigMap (persistence across restarts) and then hot-adds via /model/new
// (no pod restart — preserves the buyer sidecar's consumed-auth state).
//
// If the hot-add API call fails, we do NOT fall back to a pod restart: that
// would wipe the sidecar's emptyDir /state/consumed.json and cause the
// facilitator to reject previously-spent auths as double-spends. Instead we
// log and rely on the ConfigMap being reloaded on the next natural restart.
func (c *Controller) addLiteLLMModelEntry(ctx context.Context, ns, modelName string) {
	cm, err := c.kubeClient.CoreV1().ConfigMaps(ns).Get(ctx, "litellm-config", metav1.GetOptions{})
	if err != nil {
		log.Printf("purchase: failed to read litellm-config: %v", err)
		return
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}

	var cfg model.LiteLLMConfig
	if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
		log.Printf("purchase: failed to parse litellm-config: %v", err)
		return
	}

	for _, entry := range cfg.ModelList {
		if entry.ModelName == modelName {
			return
		}
	}

	entry := model.ModelEntry{
		ModelName: modelName,
		LiteLLMParams: model.LiteLLMParams{
			Model:   "openai/" + modelName,
			APIBase: "http://127.0.0.1:8402/v1",
			APIKey:  "unused",
		},
	}
	cfg.ModelList = append(cfg.ModelList, entry)

	rendered, err := yaml.Marshal(&cfg)
	if err != nil {
		log.Printf("purchase: failed to serialize litellm-config: %v", err)
		return
	}

	cm.Data["config.yaml"] = string(rendered)
	if _, err := c.kubeClient.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		log.Printf("purchase: failed to update litellm-config: %v", err)
		return
	}

	if err := c.hotAddLiteLLMModel(ctx, ns, entry); err != nil {
		// Secret missing is a legitimate "API not available" signal — LiteLLM
		// will pick the model up on its next reload of config.yaml.
		if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
			log.Printf("purchase: hot-add skipped (%v); model will load on next config reload", err)
			return
		}
		log.Printf("purchase: hot-add %s failed: %v; relying on ConfigMap reload", modelName, err)
	}
}

func preSignedAuthMaps(pr *monetizeapi.PurchaseRequest) ([]map[string]string, error) {
	if len(pr.Spec.PreSignedAuths) == 0 {
		return nil, fmt.Errorf("no pre-signed auths in spec")
	}

	auths := make([]map[string]string, len(pr.Spec.PreSignedAuths))
	for i, a := range pr.Spec.PreSignedAuths {
		auths[i] = map[string]string{
			"signature":   normalizeRecoverySignature(a.Signature),
			"from":        a.From,
			"to":          a.To,
			"value":       a.Value,
			"validAfter":  a.ValidAfter,
			"validBefore": a.ValidBefore,
			"nonce":       a.Nonce,
		}
	}

	return auths, nil
}

// removeLiteLLMModelEntry drops a paid/<model> route from LiteLLM. Mirrors
// addLiteLLMModelEntry: ConfigMap patch (persistence) + hot-delete via the
// /model/delete API (no pod restart). See addLiteLLMModelEntry for the
// rationale on not falling back to a rollout restart.
func (c *Controller) removeLiteLLMModelEntry(ctx context.Context, ns, modelName string) {
	cm, err := c.kubeClient.CoreV1().ConfigMaps(ns).Get(ctx, "litellm-config", metav1.GetOptions{})
	if err != nil {
		log.Printf("purchase: remove model: failed to read litellm-config: %v", err)
		return
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}

	var cfg model.LiteLLMConfig
	if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
		log.Printf("purchase: remove model: failed to parse litellm-config: %v", err)
		return
	}

	filtered := cfg.ModelList[:0]
	changed := false
	for _, entry := range cfg.ModelList {
		if entry.ModelName == modelName {
			changed = true
			continue
		}
		filtered = append(filtered, entry)
	}
	if changed {
		cfg.ModelList = filtered

		rendered, err := yaml.Marshal(&cfg)
		if err != nil {
			log.Printf("purchase: remove model: failed to serialize litellm-config: %v", err)
			return
		}
		cm.Data["config.yaml"] = string(rendered)
		if _, err := c.kubeClient.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
			log.Printf("purchase: remove model: failed to update litellm-config: %v", err)
			return
		}
	}

	if err := c.hotDeleteLiteLLMModel(ctx, ns, modelName); err != nil {
		if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
			log.Printf("purchase: hot-delete skipped (%v); model will drop on next config reload", err)
			return
		}
		log.Printf("purchase: hot-delete %s failed: %v; relying on ConfigMap reload", modelName, err)
	}
}

// triggerBuyerReload sends POST /admin/reload to the x402-buyer sidecar
// on all running litellm pods. Best-effort — the sidecar reloads on its
// own 5-second timer anyway.
func (c *Controller) triggerBuyerReload(ctx context.Context, ns string) {
	pods, err := c.kubeClient.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "app=litellm",
	})
	if err != nil || len(pods.Items) == 0 {
		return
	}

	for _, pod := range pods.Items {
		if pod.Status.Phase != "Running" || pod.Status.PodIP == "" {
			continue
		}
		reloadURL := fmt.Sprintf("http://%s:8402/admin/reload", pod.Status.PodIP)
		reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		req, _ := http.NewRequestWithContext(reqCtx, "POST", reloadURL, nil)
		c.httpClient.Do(req) //nolint:bodyclose // best-effort, response ignored
		cancel()
	}
}

// ── Sidecar status check ────────────────────────────────────────────────────

func (c *Controller) checkBuyerStatus(ctx context.Context, ns, name string) (remaining, spent int, err error) {
	// List LiteLLM pods to get a pod IP.
	pods, err := c.kubeClient.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "app=litellm",
	})
	if err != nil || len(pods.Items) == 0 {
		return 0, 0, fmt.Errorf("no litellm pods in %s", ns)
	}

	for _, pod := range pods.Items {
		if pod.Status.Phase != "Running" || pod.Status.PodIP == "" {
			continue
		}

		resp, err := c.httpClient.Get(fmt.Sprintf("http://%s:8402/status", pod.Status.PodIP))
		if err != nil {
			continue
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		var status map[string]struct {
			Remaining int `json:"remaining"`
			Spent     int `json:"spent"`
		}
		if err := json.Unmarshal(body, &status); err != nil {
			continue
		}

		if info, ok := status[name]; ok {
			return info.Remaining, info.Spent, nil
		}
	}

	return 0, 0, fmt.Errorf("upstream %q not found in sidecar status", name)
}

func normalizeRecoverySignature(sig string) string {
	if len(sig) != 132 || !strings.HasPrefix(sig, "0x") {
		return sig
	}

	lastByte, err := strconv.ParseUint(sig[len(sig)-2:], 16, 8)
	if err != nil {
		return sig
	}
	if lastByte <= 1 {
		return sig[:len(sig)-2] + fmt.Sprintf("%02x", lastByte+27)
	}

	return sig
}

func normalizePurchasedUpstreamURL(endpoint string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	for _, suffix := range []string{"/v1/chat/completions", "/chat/completions"} {
		if strings.HasSuffix(trimmed, suffix) {
			return strings.TrimSuffix(trimmed, suffix)
		}
	}

	return trimmed
}

