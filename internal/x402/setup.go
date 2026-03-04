package x402

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"gopkg.in/yaml.v3"
)

const (
	x402Namespace    = "x402"
	pricingConfigMap = "x402-pricing"
	x402SecretName   = "x402-secrets"
)

// x402Manifest returns the Kubernetes manifest for the x402 verifier subsystem.
// In development mode (OBOL_DEVELOPMENT=true) the image pull policy is IfNotPresent
// so locally-built images imported via k3d are used. Otherwise it is Always so the
// image is pulled from GHCR.
var x402Manifest = []byte(`apiVersion: v1
kind: Namespace
metadata:
  name: x402
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: x402-pricing
  namespace: x402
data:
  pricing.yaml: |
    wallet: ""
    chain: "base-sepolia"
    facilitatorURL: "https://facilitator.x402.rs"
    verifyOnly: false
    routes: []
---
apiVersion: v1
kind: Secret
metadata:
  name: x402-secrets
  namespace: x402
type: Opaque
stringData:
  WALLET_ADDRESS: ""
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: x402-verifier
  namespace: x402
  labels:
    app: x402-verifier
  annotations:
    configmap.reloader.stakater.com/reload: "x402-pricing"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: x402-verifier
  template:
    metadata:
      labels:
        app: x402-verifier
    spec:
      containers:
        - name: verifier
          image: ghcr.io/obolnetwork/x402-verifier:799fff1
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
          args:
            - --config=/config/pricing.yaml
            - --listen=:8080
          volumeMounts:
            - name: pricing-config
              mountPath: /config
              readOnly: true
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 3
            periodSeconds: 5
            timeoutSeconds: 2
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 10
            periodSeconds: 10
            timeoutSeconds: 2
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 500m
              memory: 256Mi
      volumes:
        - name: pricing-config
          configMap:
            name: x402-pricing
            items:
              - key: pricing.yaml
                path: pricing.yaml
---
apiVersion: v1
kind: Service
metadata:
  name: x402-verifier
  namespace: x402
  labels:
    app: x402-verifier
spec:
  type: ClusterIP
  selector:
    app: x402-verifier
  ports:
    - name: http
      port: 8080
      targetPort: http
      protocol: TCP
---
# RBAC: namespace-scoped pricing ConfigMap access for OpenClaw agents.
# Deployed alongside the namespace so it's always present when x402 exists.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: openclaw-x402-pricing
  namespace: x402
rules:
  - apiGroups: [""]
    resources: ["configmaps"]
    resourceNames: ["x402-pricing"]
    verbs: ["get", "list", "update", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: openclaw-x402-pricing-binding
  namespace: x402
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: openclaw-x402-pricing
subjects:
  - kind: ServiceAccount
    name: openclaw
    namespace: openclaw-obol-agent
`)

// EnsureVerifier deploys the x402 verifier subsystem if it doesn't exist.
// Idempotent — kubectl apply is safe to run multiple times.
func EnsureVerifier(cfg *config.Config) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}
	bin, kc := kubectl.Paths(cfg)

	// Quick check: if the namespace already exists, skip the apply.
	if _, err := kubectl.Output(bin, kc, "get", "namespace", x402Namespace, "--no-headers"); err == nil {
		return nil
	}

	fmt.Println("Deploying x402 payment verifier...")
	return kubectl.Apply(bin, kc, x402Manifest)
}


// Setup configures x402 pricing in the cluster by patching the ConfigMap
// and Secret. Stakater Reloader auto-restarts the verifier pod.
// If facilitatorURL is empty, the default (https://facilitator.x402.rs) is used.
func Setup(cfg *config.Config, wallet, chain, facilitatorURL string) error {
	if err := ValidateWallet(wallet); err != nil {
		return err
	}
	if err := EnsureVerifier(cfg); err != nil {
		return fmt.Errorf("deploy x402 verifier: %w", err)
	}
	bin, kc := kubectl.Paths(cfg)

	// 1. Patch the Secret with the wallet address.
	fmt.Printf("Configuring x402: setting wallet address...\n")
	secretPatch := map[string]any{"stringData": map[string]string{"WALLET_ADDRESS": wallet}}
	patchJSON, err := json.Marshal(secretPatch)
	if err != nil {
		return fmt.Errorf("marshal secret patch: %w", err)
	}
	if err := kubectl.Run(bin, kc,
		"patch", "secret", x402SecretName, "-n", x402Namespace,
		"-p", string(patchJSON), "--type=merge"); err != nil {
		return fmt.Errorf("failed to patch x402 secret: %w", err)
	}

	// 2. Update the pricing ConfigMap with wallet and chain.
	// Read existing config to preserve routes added by the ServiceOffer reconciler.
	fmt.Printf("Updating x402 pricing config...\n")
	if facilitatorURL == "" {
		facilitatorURL = "https://facilitator.x402.rs"
	}
	existingCfg, _ := GetPricingConfig(cfg)
	var existingRoutes []RouteRule
	if existingCfg != nil {
		existingRoutes = existingCfg.Routes
	}
	pricingCfg := &PricingConfig{
		Wallet:         wallet,
		Chain:          chain,
		FacilitatorURL: facilitatorURL,
		VerifyOnly:     false,
		Routes:         existingRoutes,
	}
	if err := patchPricingConfig(bin, kc, pricingCfg); err != nil {
		return fmt.Errorf("failed to patch x402 pricing: %w", err)
	}

	fmt.Printf("x402 configured: wallet=%s chain=%s\n", wallet, chain)
	return nil
}

// AddRoute adds a pricing route to the x402 ConfigMap.
// Optional per-route payTo and network override the global config when set.
func AddRoute(cfg *config.Config, pattern, price, description string, opts ...RouteOption) error {
	if err := EnsureVerifier(cfg); err != nil {
		return fmt.Errorf("deploy x402 verifier: %w", err)
	}

	// Read current pricing config.
	pricingCfg, err := GetPricingConfig(cfg)
	if err != nil {
		return fmt.Errorf("read pricing config: %w", err)
	}

	// Build the route rule.
	rule := RouteRule{
		Pattern:     pattern,
		Price:       price,
		Description: description,
	}
	for _, opt := range opts {
		opt(&rule)
	}

	pricingCfg.Routes = append(pricingCfg.Routes, rule)

	// Re-serialize and patch.
	bin, kc := kubectl.Paths(cfg)
	return patchPricingConfig(bin, kc, pricingCfg)
}

// RouteOption is a functional option for AddRoute.
type RouteOption func(*RouteRule)

// WithPayTo sets a per-route payTo address (overrides global wallet).
func WithPayTo(payTo string) RouteOption {
	return func(r *RouteRule) { r.PayTo = payTo }
}

// WithNetwork sets a per-route network (overrides global chain).
func WithNetwork(network string) RouteOption {
	return func(r *RouteRule) { r.Network = network }
}

// GetPricingConfig reads the current x402 pricing ConfigMap from the cluster.
func GetPricingConfig(cfg *config.Config) (*PricingConfig, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return nil, err
	}
	bin, kc := kubectl.Paths(cfg)

	raw, err := kubectl.Output(bin, kc,
		"get", "configmap", pricingConfigMap, "-n", x402Namespace,
		"-o", `jsonpath={.data.pricing\.yaml}`)
	if err != nil {
		// x402 namespace/configmap doesn't exist yet — not an error, just no config.
		return &PricingConfig{}, nil
	}

	if strings.TrimSpace(raw) == "" {
		return &PricingConfig{}, nil
	}

	// Write to temp file and load via existing parser.
	tmpFile, err := os.CreateTemp("", "x402-pricing-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(raw); err != nil {
		tmpFile.Close()
		return nil, err
	}
	tmpFile.Close()

	return LoadConfig(tmpFile.Name())
}

// WritePricingConfig writes the pricing config to the cluster ConfigMap.
func WritePricingConfig(cfg *config.Config, pcfg *PricingConfig) error {
	bin, kc := kubectl.Paths(cfg)
	return patchPricingConfig(bin, kc, pcfg)
}

func patchPricingConfig(bin, kc string, pcfg *PricingConfig) error {
	pricingBytes, err := yaml.Marshal(pcfg)
	if err != nil {
		return fmt.Errorf("marshal pricing config: %w", err)
	}

	cmPatch := map[string]any{
		"data": map[string]string{
			"pricing.yaml": string(pricingBytes),
		},
	}
	cmPatchJSON, err := json.Marshal(cmPatch)
	if err != nil {
		return fmt.Errorf("marshal pricing patch: %w", err)
	}

	return kubectl.Run(bin, kc,
		"patch", "configmap", pricingConfigMap, "-n", x402Namespace,
		"-p", string(cmPatchJSON), "--type=merge")
}
