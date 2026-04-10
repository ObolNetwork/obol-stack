package serviceoffercontroller

import (
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"gopkg.in/yaml.v3"
)

const (
	buyerConfigCM = "x402-buyer-config"
	buyerAuthsCM  = "x402-buyer-auths"
)

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

	cfg.ModelList = append(cfg.ModelList, model.ModelEntry{
		ModelName: modelName,
		LiteLLMParams: model.LiteLLMParams{
			Model:   "openai/" + modelName,
			APIBase: "http://127.0.0.1:8402",
			APIKey:  "unused",
		},
	})

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

	c.restartLiteLLM(ctx, ns)
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
	if !changed {
		return
	}
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

	c.restartLiteLLM(ctx, ns)
}

func (c *Controller) restartLiteLLM(ctx context.Context, ns string) {
	deploy, err := c.kubeClient.AppsV1().Deployments(ns).Get(ctx, "litellm", metav1.GetOptions{})
	if err != nil {
		log.Printf("purchase: failed to get litellm deployment: %v", err)
		return
	}
	if deploy.Spec.Template.Annotations == nil {
		deploy.Spec.Template.Annotations = make(map[string]string)
	}
	deploy.Spec.Template.Annotations["obol.org/restartedAt"] = time.Now().UTC().Format(time.RFC3339)
	if _, err := c.kubeClient.AppsV1().Deployments(ns).Update(ctx, deploy, metav1.UpdateOptions{}); err != nil {
		log.Printf("purchase: failed to restart litellm: %v", err)
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

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://%s:8402/status", pod.Status.PodIP))
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

// ── Condition helpers ───────────────────────────────────────────────────────

func conditionIsTrue(conditions []monetizeapi.Condition, condType string) bool {
	for _, c := range conditions {
		if c.Type == condType {
			return c.Status == "True"
		}
	}
	return false
}
