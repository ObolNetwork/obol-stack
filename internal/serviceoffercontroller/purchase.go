package serviceoffercontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const purchaseRequestFinalizer = "obol.org/purchase-finalizer"

// ── PurchaseRequest reconciler ──────────────────────────────────────────────

func (c *Controller) reconcilePurchase(ctx context.Context, key string) error {
	ns, name, _ := strings.Cut(key, "/")

	raw, err := c.dynClient.Resource(monetizeapi.PurchaseRequestGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil // deleted
	}

	var pr monetizeapi.PurchaseRequest
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &pr); err != nil {
		return fmt.Errorf("unmarshal PurchaseRequest: %w", err)
	}

	// Add finalizer if missing.
	if !hasStringInSlice(raw.GetFinalizers(), purchaseRequestFinalizer) {
		patched := raw.DeepCopy()
		patched.SetFinalizers(append(patched.GetFinalizers(), purchaseRequestFinalizer))
		if _, err := c.dynClient.Resource(monetizeapi.PurchaseRequestGVR).Namespace(ns).Update(ctx, patched, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("add finalizer: %w", err)
		}
		return nil
	}

	// Handle deletion.
	if raw.GetDeletionTimestamp() != nil {
		return c.reconcileDeletingPurchase(ctx, &pr, raw)
	}

	status := pr.Status
	if pr.Status.ObservedGeneration != pr.Generation {
		status = monetizeapi.PurchaseRequestStatus{}
	}
	status.ObservedGeneration = pr.Generation
	status.Conditions = append([]monetizeapi.Condition{}, status.Conditions...)

	// Stage 1: Probe
	if !purchaseConditionIsTrue(status.Conditions, "Probed") {
		if err := c.reconcilePurchaseProbe(ctx, &status, &pr); err != nil {
			log.Printf("purchase %s/%s: probe failed: %v", ns, name, err)
		}
	}

	// Stage 2: Sign auths
	if purchaseConditionIsTrue(status.Conditions, "Probed") && !purchaseConditionIsTrue(status.Conditions, "AuthsSigned") {
		if err := c.reconcilePurchaseSign(ctx, &status, &pr); err != nil {
			log.Printf("purchase %s/%s: sign failed: %v", ns, name, err)
		}
	}

	// Stage 3: Configure sidecar
	if purchaseConditionIsTrue(status.Conditions, "AuthsSigned") && !purchaseConditionIsTrue(status.Conditions, "Configured") {
		if err := c.reconcilePurchaseConfigure(ctx, &status, &pr); err != nil {
			log.Printf("purchase %s/%s: configure failed: %v", ns, name, err)
		}
	}

	// Stage 4: Verify sidecar loaded
	if purchaseConditionIsTrue(status.Conditions, "Configured") {
		c.reconcilePurchaseReady(ctx, &status, &pr)
	}

	ready := purchaseConditionIsTrue(status.Conditions, "Ready")
	if err := c.updatePurchaseStatus(ctx, raw, &status); err != nil {
		return err
	}
	if !ready {
		// ConfigMap projection and sidecar reload are asynchronous; requeue so
		// readiness can advance without requiring a CR spec/status mutation.
		c.purchaseQueue.AddAfter(key, 5*time.Second)
	}
	return nil
}

func (c *Controller) reconcileDeletingPurchase(ctx context.Context, pr *monetizeapi.PurchaseRequest, raw *unstructured.Unstructured) error {
	buyerNS := pr.EffectiveBuyerNamespace()
	c.removeLiteLLMModelEntry(ctx, buyerNS, "paid/"+pr.Spec.Model)
	c.removeBuyerUpstream(ctx, buyerNS, pr.Name)

	patched := raw.DeepCopy()
	fins := patched.GetFinalizers()
	filtered := fins[:0]
	for _, f := range fins {
		if f != purchaseRequestFinalizer {
			filtered = append(filtered, f)
		}
	}
	patched.SetFinalizers(filtered)
	_, err := c.dynClient.Resource(monetizeapi.PurchaseRequestGVR).Namespace(pr.Namespace).Update(ctx, patched, metav1.UpdateOptions{})
	return err
}

// ── Stage 1: Probe ──────────────────────────────────────────────────────────

func (c *Controller) reconcilePurchaseProbe(ctx context.Context, status *monetizeapi.PurchaseRequestStatus, pr *monetizeapi.PurchaseRequest) error {
	client := &http.Client{Timeout: 15 * time.Second}

	body := `{"model":"probe","messages":[{"role":"user","content":"probe"}],"max_tokens":1}`
	req, err := http.NewRequestWithContext(ctx, "POST", pr.Spec.Endpoint, strings.NewReader(body))
	if err != nil {
		setPurchaseCondition(&status.Conditions, "Probed", "False", "ProbeError", err.Error())
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		setPurchaseCondition(&status.Conditions, "Probed", "False", "ProbeError", err.Error())
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusPaymentRequired {
		setPurchaseCondition(&status.Conditions, "Probed", "False", "NotPaymentGated",
			fmt.Sprintf("expected 402, got %d", resp.StatusCode))
		return fmt.Errorf("expected 402, got %d", resp.StatusCode)
	}

	var pricing struct {
		Accepts []struct {
			PayTo             string `json:"payTo"`
			MaxAmountRequired string `json:"maxAmountRequired"`
			Amount            string `json:"amount"`
			Network           string `json:"network"`
			Asset             string `json:"asset"`
			Extra             struct {
				Name                string `json:"name"`
				Version             string `json:"version"`
				AssetTransferMethod string `json:"assetTransferMethod"`
			} `json:"extra"`
		} `json:"accepts"`
	}
	if err := json.Unmarshal(respBody, &pricing); err != nil || len(pricing.Accepts) == 0 {
		setPurchaseCondition(&status.Conditions, "Probed", "False", "InvalidPricing", "402 body missing accepts")
		return fmt.Errorf("invalid 402 response")
	}

	accept := pricing.Accepts[0]
	price := accept.Amount
	if price == "" {
		price = accept.MaxAmountRequired
	}
	if price != pr.Spec.Payment.Price {
		setPurchaseCondition(&status.Conditions, "Probed", "False", "PricingMismatch",
			fmt.Sprintf("spec.price=%s but endpoint wants %s", pr.Spec.Payment.Price, price))
		return fmt.Errorf("pricing mismatch")
	}

	status.ProbedAt = time.Now().UTC().Format(time.RFC3339)
	status.ProbedPrice = price
	pr.Spec.Payment.Asset = accept.Asset
	pr.Spec.Payment.AssetTransferMethod = accept.Extra.AssetTransferMethod
	pr.Spec.Payment.EIP712Name = accept.Extra.Name
	pr.Spec.Payment.EIP712Version = accept.Extra.Version
	setPurchaseCondition(&status.Conditions, "Probed", "True", "Validated",
		fmt.Sprintf("402: %s on %s", price, accept.Network))
	return nil
}

// ── Stage 2: Read pre-signed auths from spec ────────────────────────────────
//
// buy.py signs the auths locally (it has remote-signer access in the same
// namespace) and embeds them in spec.preSignedAuths. The controller reads
// them directly from the CR — no cross-namespace Secret read needed.

func (c *Controller) reconcilePurchaseSign(ctx context.Context, status *monetizeapi.PurchaseRequestStatus, pr *monetizeapi.PurchaseRequest) error {
	auths, err := preSignedAuthMaps(pr)
	if err != nil {
		setPurchaseCondition(&status.Conditions, "AuthsSigned", "False", "NoAuths",
			"spec.preSignedAuths is empty — buy.py should embed auths in the CR")
		return err
	}

	if signer := purchaseSignerAddress(pr.Spec.PreSignedAuths[0]); signer != "" {
		status.SignerAddress = signer
	}

	c.pendingAuths.Store(pr.Namespace+"/"+pr.Name, auths)
	status.TotalSigned = len(auths)
	setPurchaseCondition(&status.Conditions, "AuthsSigned", "True", "Loaded",
		fmt.Sprintf("Loaded %d pre-signed auths from spec", len(auths)))
	return nil
}

// ── Stage 3: Configure sidecar ──────────────────────────────────────────────

func (c *Controller) reconcilePurchaseConfigure(ctx context.Context, status *monetizeapi.PurchaseRequestStatus, pr *monetizeapi.PurchaseRequest) error {
	key := pr.Namespace + "/" + pr.Name
	authsRaw, ok := c.pendingAuths.Load(key)
	var auths []map[string]any
	var err error
	if ok {
		auths = authsRaw.([]map[string]any)
		c.pendingAuths.Delete(key)
	} else {
		// Rebuild from spec so crash-restart does not wedge the request.
		auths, err = preSignedAuthMaps(pr)
		if err != nil {
			setPurchaseCondition(&status.Conditions, "Configured", "False", "NoAuths", "No auths available to write")
			return err
		}
	}

	buyerNS := pr.EffectiveBuyerNamespace()

	upstream := map[string]any{
		"url":                 normalizePurchasedUpstreamURL(pr.Spec.Endpoint),
		"network":             pr.Spec.Payment.Network,
		"payTo":               pr.Spec.Payment.PayTo,
		"price":               pr.Spec.Payment.Price,
		"asset":               pr.Spec.Payment.Asset,
		"assetSymbol":         pr.Spec.Payment.AssetSymbol,
		"assetDecimals":       pr.Spec.Payment.AssetDecimals,
		"assetTransferMethod": pr.Spec.Payment.AssetTransferMethod,
		"eip712Name":          pr.Spec.Payment.EIP712Name,
		"eip712Version":       pr.Spec.Payment.EIP712Version,
		"remoteModel":         pr.Spec.Model,
	}

	if err := c.mergeBuyerConfig(ctx, buyerNS, pr.Name, upstream); err != nil {
		setPurchaseCondition(&status.Conditions, "Configured", "False", "ConfigWriteError", err.Error())
		return err
	}

	if err := c.mergeBuyerAuths(ctx, buyerNS, pr.Name, auths); err != nil {
		setPurchaseCondition(&status.Conditions, "Configured", "False", "AuthsWriteError", err.Error())
		return err
	}

	// Trigger immediate sidecar reload so it picks up the new config/auths
	// without waiting for the 5-second ticker.
	c.triggerBuyerReload(ctx, buyerNS)

	// Hot-add via /model/new API — no pod restart needed.
	paidModel := "paid/" + pr.Spec.Model
	c.addLiteLLMModelEntry(ctx, buyerNS, paidModel)

	status.Remaining = len(auths)
	status.PublicModel = paidModel
	setPurchaseCondition(&status.Conditions, "Configured", "True", "Written",
		fmt.Sprintf("Wrote %d auths to %s/x402-buyer-auths", len(auths), buyerNS))
	return nil
}

// ── Stage 4: Ready ──────────────────────────────────────────────────────────

func (c *Controller) reconcilePurchaseReady(ctx context.Context, status *monetizeapi.PurchaseRequestStatus, pr *monetizeapi.PurchaseRequest) {
	buyerNS := pr.EffectiveBuyerNamespace()

	remaining, spent, err := c.checkBuyerStatus(ctx, buyerNS, pr.Name)
	if err != nil {
		setPurchaseCondition(&status.Conditions, "Ready", "False", "SidecarNotReady", err.Error())
		return
	}

	status.Remaining = remaining
	status.Spent = spent
	setPurchaseCondition(&status.Conditions, "Ready", "True", "Reconciled",
		fmt.Sprintf("Sidecar: %d remaining, %d spent", remaining, spent))
}

// ── Status helpers ──────────────────────────────────────────────────────────

func (c *Controller) updatePurchaseStatus(ctx context.Context, raw *unstructured.Unstructured, status *monetizeapi.PurchaseRequestStatus) error {
	patched := raw.DeepCopy()
	statusObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(status)
	if err != nil {
		return err
	}
	if existing, found := patched.Object["status"]; found && equality.Semantic.DeepEqual(existing, statusObj) {
		return nil
	}
	patched.Object["status"] = statusObj
	_, err = c.dynClient.Resource(monetizeapi.PurchaseRequestGVR).
		Namespace(patched.GetNamespace()).
		UpdateStatus(ctx, patched, metav1.UpdateOptions{})
	return err
}

func hasStringInSlice(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

func purchaseConditionIsTrue(conditions []monetizeapi.Condition, condType string) bool {
	for _, c := range conditions {
		if c.Type == condType {
			return c.Status == "True"
		}
	}
	return false
}

func setPurchaseCondition(conditions *[]monetizeapi.Condition, condType, status, reason, message string) {
	now := metav1.Now()
	for i, c := range *conditions {
		if c.Type == condType {
			if c.Status != status {
				(*conditions)[i].LastTransitionTime = now
			}
			(*conditions)[i].Status = status
			(*conditions)[i].Reason = reason
			(*conditions)[i].Message = message
			return
		}
	}
	*conditions = append(*conditions, monetizeapi.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}
