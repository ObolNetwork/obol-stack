package hermes

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/dns"
	obolembed "github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	petname "github.com/dustinkirkland/golang-petname"
	"gopkg.in/yaml.v3"
)

const (
	valuesFileName       = "values-hermes.yaml"
	helmfileFileName     = "helmfile.yaml"
	gatewayTokenFileName = ".gateway-token"
	obolSkillsDirName    = "obol-skills"

	// renovate: datasource=helm depName=raw registryUrl=https://bedag.github.io/helm-charts/
	rawChartVersion = "2.0.2"

	// renovate: datasource=docker depName=nousresearch/hermes-agent
	defaultImage = "nousresearch/hermes-agent:v2026.7.1"
	// Use the upstream image venv instead of cloning Hermes into the PVC on
	// every cold start. The init container below validates the required extras
	// are present so image regressions fail before the gateway starts.
	hermesBinary = "/opt/hermes/.venv/bin/hermes"

	containerUID  = 1000
	containerGID  = 1000
	dashboardPort = 9119
)

type OnboardOptions struct {
	ID        string
	Force     bool
	Sync      bool
	IsDefault bool
	AgentMode bool
}

type SetupOptions struct{}

type DashboardOptions struct {
	Port      int
	NoBrowser bool
}

type instance struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	URL       string `json:"url"`
}

func DeploymentPath(cfg *config.Config, id string) string {
	return agentruntime.DeploymentPath(cfg, agentruntime.Hermes, id)
}

func SetupDefault(cfg *config.Config, u *ui.UI) error {
	if _, _, err := configuredModels(cfg, u); err != nil {
		u.Warnf("Skipping default Hermes agent: %v", err)
		u.Print("  Run 'obol model setup' to configure LiteLLM, then 'obol agent init'.")
		return nil
	}

	return Onboard(cfg, OnboardOptions{
		ID:        agentruntime.DefaultInstanceID,
		Sync:      true,
		IsDefault: true,
		AgentMode: true,
	}, u)
}

func Onboard(cfg *config.Config, opts OnboardOptions, u *ui.UI) error {
	id := strings.TrimSpace(opts.ID)
	if opts.IsDefault {
		id = agentruntime.DefaultInstanceID
	}

	if id == "" {
		id = petname.Generate(2, "-")
		u.Infof("Generated deployment ID: %s", id)
	} else {
		u.Infof("Using deployment ID: %s", id)
	}

	deploymentDir := DeploymentPath(cfg, id)
	namespace := agentruntime.Namespace(agentruntime.Hermes, id)
	hostname := agentruntime.Hostname(agentruntime.Hermes, id)

	if _, err := os.Stat(deploymentDir); err == nil && !opts.Force && !opts.IsDefault {
		return fmt.Errorf("deployment already exists: hermes/%s\nDirectory: %s\nUse --force or -f to overwrite", id, deploymentDir)
	}

	if opts.IsDefault && !opts.Force {
		if _, err := os.Stat(deploymentDir); err == nil {
			u.Info("Default Hermes instance already configured, re-syncing...")
			if err := dns.EnsureHostsEntries(agentruntime.CollectHostnames(cfg, agentruntime.DeploymentRef{
				Runtime: agentruntime.Hermes,
				ID:      id,
			})); err != nil {
				u.Warnf("Could not update /etc/hosts for Hermes hostnames: %v", err)
			}
			if err := writeDeploymentFiles(cfg, id, deploymentDir, currentAgentBaseURL(deploymentDir), u); err != nil {
				return err
			}
			if opts.Sync {
				return Sync(cfg, id, u)
			}
			return nil
		}
	}

	if _, err := os.Stat(deploymentDir); err == nil && opts.Force {
		u.Warnf("Overwriting existing deployment at %s", deploymentDir)
	}

	if err := os.MkdirAll(deploymentDir, 0o755); err != nil {
		return fmt.Errorf("failed to create deployment directory: %w", err)
	}

	if err := dns.EnsureHostsEntries(agentruntime.CollectHostnames(cfg, agentruntime.DeploymentRef{
		Runtime: agentruntime.Hermes,
		ID:      id,
	})); err != nil {
		u.Warnf("Could not update /etc/hosts for Hermes hostnames: %v", err)
	}

	u.Blank()
	u.Info("Generating Ethereum wallet...")
	wallet, err := GenerateWallet(cfg, id, u)
	if err != nil {
		_ = os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to generate wallet: %w", err)
	}

	rsValues := generateRemoteSignerValues(wallet)
	if err := os.WriteFile(filepath.Join(deploymentDir, "values-remote-signer.yaml"), []byte(rsValues), 0o600); err != nil {
		_ = os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write remote-signer values: %w", err)
	}

	if err := WriteWalletMetadata(deploymentDir, wallet); err != nil {
		_ = os.RemoveAll(deploymentDir)
		return fmt.Errorf("failed to write wallet metadata: %w", err)
	}

	agentBaseURL := ""
	if opts.AgentMode {
		if st, _ := tunnel.LoadTunnelState(cfg); st != nil && st.IsPersistent() {
			agentBaseURL = "https://" + st.Hostname
		}
	}

	if err := writeDeploymentFiles(cfg, id, deploymentDir, agentBaseURL, u); err != nil {
		_ = os.RemoveAll(deploymentDir)
		return err
	}

	u.Blank()
	u.Success("Hermes instance configured!")
	u.Detail("Deployment", fmt.Sprintf("hermes/%s", id))
	u.Detail("Namespace", namespace)
	u.Detail("Hostname", hostname)
	u.Detail("Wallet", wallet.Address)
	u.Detail("Location", deploymentDir)
	u.Blank()
	u.Print("Files created:")
	u.Print("  - values-hermes.yaml         Hermes deployment manifest")
	u.Print("  - values-remote-signer.yaml  Remote-signer config")
	u.Print("  - wallet.json                Wallet metadata")
	u.Print("  - helmfile.yaml              Hermes + remote-signer deployment configuration")
	u.Blank()
	u.Print("  Back up your signing key:")
	u.Printf("    cp -r %s ~/obol-wallet-backup/", agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, id))

	if opts.Sync {
		u.Blank()
		u.Info("Deploying to cluster...")
		u.Blank()
		return Sync(cfg, id, u)
	}

	u.Printf("\nTo deploy: obol agent sync %s", id)
	return nil
}

func Sync(cfg *config.Config, id string, u *ui.UI) error {
	deploymentDir := DeploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: hermes/%s\nDirectory: %s", id, deploymentDir)
	}

	if err := dns.EnsureHostsEntries(agentruntime.CollectHostnames(cfg, agentruntime.DeploymentRef{
		Runtime: agentruntime.Hermes,
		ID:      id,
	})); err != nil {
		u.Warnf("Could not update /etc/hosts for Hermes hostnames: %v", err)
	}

	if err := writeDeploymentFiles(cfg, id, deploymentDir, currentAgentBaseURL(deploymentDir), u); err != nil {
		return err
	}

	helmfilePath := filepath.Join(deploymentDir, helmfileFileName)
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		return fmt.Errorf("helmfile.yaml not found in: %s", deploymentDir)
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	if err := refreshHelmRepos(cfg); err != nil {
		return err
	}

	// Clear any stale strategy.rollingUpdate before helm runs. A hermes
	// deployment first created with the default RollingUpdate strategy keeps a
	// k8s-defaulted rollingUpdate that helm won't remove when the manifest flips
	// type to Recreate, so the upgrade is rejected and every obol model
	// setup/prefer/sync silently fails to reach the running agent.
	migrateDeploymentStrategy(cfg, agentruntime.Namespace(agentruntime.Hermes, id), u)

	helmfileBinary := filepath.Join(cfg.BinDir, "helmfile")
	syncArgs := append([]string{"-f", helmfilePath, "sync"}, helmcmd.SyncFlagsForVersion(filepath.Join(cfg.BinDir, "helm"))...)
	cmd := exec.Command(helmfileBinary, syncArgs...)
	cmd.Dir = deploymentDir
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	u.Infof("Syncing Hermes: hermes/%s", id)
	u.Detail("Deployment directory", deploymentDir)

	if err := u.Exec(ui.ExecConfig{
		Name: "Running helmfile sync",
		Cmd:  cmd,
	}); err != nil {
		return fmt.Errorf("helmfile sync failed: %w", err)
	}

	// Publish wallet-metadata ConfigMap for the frontend (namespace now exists).
	applyWalletMetadataConfigMap(cfg, id, deploymentDir)

	u.Blank()
	u.Success("Hermes installed successfully!")
	u.Detail("Namespace", agentruntime.Namespace(agentruntime.Hermes, id))
	u.Detail("URL", "http://"+agentruntime.Hostname(agentruntime.Hermes, id))
	u.Detail("Dashboard", "http://"+dashboardHostname(id))
	u.Blank()
	u.Dim("[Optional] Retrieve an API server token:")
	u.Printf("  obol agent auth %s", id)
	u.Blank()
	u.Dim("[Optional] Port-forward fallback:")
	u.Printf("  obol kubectl -n %s port-forward svc/%s %d:%d",
		agentruntime.Namespace(agentruntime.Hermes, id),
		agentruntime.Describe(agentruntime.Hermes).ServiceName,
		agentruntime.Describe(agentruntime.Hermes).DefaultPort,
		agentruntime.Describe(agentruntime.Hermes).DefaultPort,
	)

	return nil
}

func Setup(cfg *config.Config, id string, _ SetupOptions, u *ui.UI) error {
	u.Info("Re-rendering Hermes config from the current LiteLLM model inventory...")
	return Sync(cfg, id, u)
}

func List(cfg *config.Config, u *ui.UI) error {
	ids, err := agentruntime.ListInstanceIDs(cfg, agentruntime.Hermes)
	if err != nil {
		return err
	}

	var instances []instance
	for _, id := range ids {
		instances = append(instances, instance{
			ID:        id,
			Namespace: agentruntime.Namespace(agentruntime.Hermes, id),
			URL:       "http://" + agentruntime.Hostname(agentruntime.Hermes, id),
		})
	}

	if u.IsJSON() {
		return u.JSON(instances)
	}

	if len(instances) == 0 {
		u.Print("No Hermes instances installed")
		u.Print("\nTo create one: obol agent new --runtime hermes")
		return nil
	}

	u.Info("Hermes instances:")
	u.Blank()
	for _, inst := range instances {
		u.Bold("  " + inst.ID)
		u.Detail("  Namespace", inst.Namespace)
		u.Detail("  URL", inst.URL)
		u.Blank()
	}
	u.Printf("Total: %d instance(s)", len(instances))
	return nil
}

func Delete(cfg *config.Config, id string, force bool, u *ui.UI) error {
	namespace := agentruntime.Namespace(agentruntime.Hermes, id)
	deploymentDir := DeploymentPath(cfg, id)

	u.Infof("Deleting Hermes: hermes/%s", id)
	u.Detail("Namespace", namespace)

	configExists := false
	if _, err := os.Stat(deploymentDir); err == nil {
		configExists = true
	}

	namespaceExists := false
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); err == nil {
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "get", "namespace", namespace)
		cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
		if err := cmd.Run(); err == nil {
			namespaceExists = true
		}
	}

	if !namespaceExists && !configExists {
		return fmt.Errorf("instance not found: %s", id)
	}

	u.Blank()
	u.Print("Resources to be deleted:")
	if namespaceExists {
		u.Printf("  [x] Kubernetes namespace: %s", namespace)
	} else {
		u.Printf("  [ ] Kubernetes namespace: %s (not found)", namespace)
	}
	if configExists {
		u.Printf("  [x] Configuration: %s", deploymentDir)
	}

	if !force && !u.Confirm("\nProceed with deletion?", false) {
		u.Print("Deletion cancelled")
		return nil
	}

	if namespaceExists {
		helmfilePath := filepath.Join(deploymentDir, helmfileFileName)
		helmfileBinary := filepath.Join(cfg.BinDir, "helmfile")
		if _, err := os.Stat(helmfilePath); err == nil {
			if _, err := os.Stat(helmfileBinary); err == nil {
				destroyCmd := exec.Command(helmfileBinary, "-f", helmfilePath, "destroy")
				destroyCmd.Dir = deploymentDir
				destroyCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
				if err := u.Exec(ui.ExecConfig{
					Name: "Removing Helm releases from " + namespace,
					Cmd:  destroyCmd,
				}); err != nil {
					u.Warnf("helmfile destroy failed (will force-delete namespace): %v", err)
				}
			}
		}

		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		deleteCmd := exec.Command(kubectlBinary, "delete", "namespace", namespace, "--force", "--grace-period=0")
		deleteCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
		if err := u.Exec(ui.ExecConfig{
			Name: "Deleting namespace " + namespace,
			Cmd:  deleteCmd,
		}); err != nil {
			u.Warnf("namespace deletion may still be in progress: %v", err)
		}
	}

	if configExists {
		u.Info("Deleting configuration...")
		if err := os.RemoveAll(deploymentDir); err != nil {
			return fmt.Errorf("failed to delete config directory: %w", err)
		}
		u.Success("Configuration deleted")
		parentDir := filepath.Join(cfg.ConfigDir, "applications", string(agentruntime.Hermes))
		if entries, err := os.ReadDir(parentDir); err == nil && len(entries) == 0 {
			_ = os.Remove(parentDir)
		}
	}

	u.Blank()
	u.Successf("Hermes %s deleted successfully!", id)
	return nil
}

func Token(cfg *config.Config, id string, u *ui.UI) error {
	token, err := getToken(cfg, id)
	if err != nil {
		return err
	}
	u.Print(token)
	return nil
}

func RegenerateToken(cfg *config.Config, id string, u *ui.UI) (string, error) {
	deploymentDir := DeploymentPath(cfg, id)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return "", fmt.Errorf("deployment not found: hermes/%s", id)
	}

	newToken, err := generateGatewayToken()
	if err != nil {
		return "", err
	}
	if err := persistGatewayToken(deploymentDir, newToken); err != nil {
		return "", err
	}
	if err := writeDeploymentFiles(cfg, id, deploymentDir, currentAgentBaseURL(deploymentDir), u); err != nil {
		return "", err
	}

	namespace := agentruntime.Namespace(agentruntime.Hermes, id)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", errors.New("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      "hermes-api-server",
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/name":       "hermes",
				"app.kubernetes.io/managed-by": "obol",
			},
		},
		"type": "Opaque",
		"stringData": map[string]string{
			"API_SERVER_KEY": newToken,
		},
	}
	raw, _ := json.Marshal(manifest) //nolint:errchkjson // controlled payload

	applyCmd := exec.Command(kubectlBinary, "apply", "-f", "-")
	applyCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	applyCmd.Stdin = bytes.NewReader(raw)
	if out, err := applyCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to apply Hermes token secret: %w\n%s", err, string(out))
	}

	restartCmd := exec.Command(kubectlBinary, "rollout", "restart", "deployment/hermes", "-n", namespace)
	restartCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	if out, err := restartCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to restart Hermes runtime: %w\n%s", err, string(out))
	}

	waitCmd := exec.Command(kubectlBinary, "rollout", "status", "deployment/hermes", "-n", namespace, "--timeout=120s")
	waitCmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	if out, err := waitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("rollout not confirmed: %w\n%s", err, string(out))
	}

	u.Success("Token regenerated successfully")
	return newToken, nil
}

// migrateDeploymentStrategy imperatively clears a stale
// spec.strategy.rollingUpdate on an existing hermes deployment so a subsequent
// helm upgrade to strategy.type=Recreate is accepted by the API server.
//
// A deployment that predates the Recreate template was created with the default
// RollingUpdate strategy, which the API auto-populates with a rollingUpdate
// block. Helm's three-way merge only patches strategy.type, leaving the stale
// rollingUpdate in place, and the API then rejects the update ("rollingUpdate
// may not be specified when strategy type is Recreate"). Emitting
// rollingUpdate: null in the manifest does not survive bedag/raw + helm's null
// handling, so a merge patch with an explicit null is the reliable fix.
//
// Best-effort: a missing deployment (fresh install) or already-Recreate
// strategy makes this a harmless no-op, so failures are warned, not fatal.
func migrateDeploymentStrategy(cfg *config.Config, namespace string, u *ui.UI) {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	cmd := exec.Command(kubectlBinary, strategyMigrationPatchArgs(namespace)...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		// NotFound (fresh install) is expected and benign; helm creates the
		// deployment fresh with the Recreate strategy. Only surface other
		// failures as a warning so sync can still proceed.
		if !strings.Contains(string(out), "NotFound") && !strings.Contains(string(out), "not found") {
			u.Warnf("Could not pre-clear hermes deployment strategy (continuing): %v\n%s", err, string(out))
		}
	}
}

// strategyMigrationPatchArgs builds the kubectl args that clear a stale
// strategy.rollingUpdate while pinning strategy.type=Recreate on the hermes
// deployment. The explicit null in a strategic merge patch is what removes the
// field that blocks the RollingUpdate -> Recreate migration.
func strategyMigrationPatchArgs(namespace string) []string {
	return []string{
		"patch", "deployment/hermes",
		"-n", namespace,
		"--type=merge",
		"-p", `{"spec":{"strategy":{"type":"Recreate","rollingUpdate":null}}}`,
	}
}

func SyncDefaultModels(cfg *config.Config, u *ui.UI) error {
	deploymentDir := DeploymentPath(cfg, agentruntime.DefaultInstanceID)
	if _, err := os.Stat(deploymentDir); os.IsNotExist(err) {
		return nil
	}
	return Sync(cfg, agentruntime.DefaultInstanceID, u)
}

func Skills(cfg *config.Config, id string, args []string) error {
	return cliViaKubectlExec(cfg, id, append([]string{"skills"}, args...))
}

func CLI(cfg *config.Config, id string, args []string) error {
	return cliViaKubectlExec(cfg, id, args)
}

func ResolveInstance(cfg *config.Config, args []string) (string, []string, error) {
	return agentruntime.ResolveInstance(cfg, agentruntime.Hermes, args)
}

func ResolveCLIInvocation(cfg *config.Config, args []string) (string, []string, error) {
	selectedID, hermesArgs, err := splitCLISelection(args)
	if err != nil {
		return "", nil, err
	}

	ids, err := agentruntime.ListInstanceIDs(cfg, agentruntime.Hermes)
	if err != nil {
		return "", nil, err
	}
	if len(ids) == 0 {
		return "", nil, errors.New("no Hermes instances found — run 'obol agent init' or 'obol agent new --runtime hermes' to create one")
	}

	if selectedID != "" {
		if containsID(ids, selectedID) {
			return selectedID, hermesArgs, nil
		}
		return "", nil, fmt.Errorf("Hermes instance %q not found; available: %s", selectedID, strings.Join(ids, ", "))
	}

	if containsID(ids, agentruntime.DefaultInstanceID) {
		return agentruntime.DefaultInstanceID, hermesArgs, nil
	}
	if len(ids) == 1 {
		return ids[0], hermesArgs, nil
	}

	return "", nil, fmt.Errorf("multiple Hermes instances found, specify one with --agent: %s", strings.Join(ids, ", "))
}

func splitCLISelection(args []string) (selectedID string, hermesArgs []string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			hermesArgs = append(hermesArgs, args[i+1:]...)
			return selectedID, hermesArgs, nil
		}

		if arg == "--agent" {
			if selectedID != "" {
				return "", nil, errors.New("--agent specified multiple times")
			}
			if i+1 >= len(args) {
				return "", nil, errors.New("--agent requires an instance name")
			}
			selectedID = strings.TrimSpace(args[i+1])
			if selectedID == "" {
				return "", nil, errors.New("--agent requires an instance name")
			}
			i++
			continue
		}

		if value, ok := strings.CutPrefix(arg, "--agent="); ok {
			if selectedID != "" {
				return "", nil, errors.New("--agent specified multiple times")
			}
			selectedID = strings.TrimSpace(value)
			if selectedID == "" {
				return "", nil, errors.New("--agent requires an instance name")
			}
			continue
		}

		hermesArgs = append(hermesArgs, arg)
	}

	return selectedID, hermesArgs, nil
}

func containsID(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func cliViaKubectlExec(cfg *config.Config, id string, args []string) error {
	return agentruntime.ExecInPod(cfg, agentruntime.Hermes, id, append([]string{hermesBinary}, args...))
}

// hermesExecArgs preserves the legacy argv-builder signature (namespace,
// in-pod hermes args, TTY flag) so existing tests stay valid. It composes
// the runtime-agnostic agentruntime.BuildExecArgs with the hermes binary
// path, deriving the instance id from the namespace suffix.
func hermesExecArgs(namespace string, args []string, withTTY bool) []string {
	id := strings.TrimPrefix(namespace, string(agentruntime.Hermes)+"-")
	return agentruntime.BuildExecArgs(
		agentruntime.Hermes,
		id,
		append([]string{hermesBinary}, args...),
		withTTY,
	)
}

func getToken(cfg *config.Config, id string) (string, error) {
	namespace := agentruntime.Namespace(agentruntime.Hermes, id)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", errors.New("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	cmd := exec.Command(kubectlBinary, "get", "secret", "hermes-api-server", "-n", namespace, "-o", "json")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get secret: %w", err)
	}

	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(out, &secret); err != nil {
		return "", fmt.Errorf("failed to parse secret: %w", err)
	}

	encoded := secret.Data["API_SERVER_KEY"]
	if encoded == "" {
		return "", fmt.Errorf("API_SERVER_KEY not found in namespace %s secrets", namespace)
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("failed to decode token: %w", err)
	}
	return string(decoded), nil
}

func writeDeploymentFiles(cfg *config.Config, id, deploymentDir, agentBaseURL string, u *ui.UI) error {
	models, primary, err := configuredModels(cfg, u)
	if err != nil {
		return err
	}

	token, err := ensureGatewayToken(deploymentDir)
	if err != nil {
		return err
	}

	namespace := agentruntime.Namespace(agentruntime.Hermes, id)
	hostname := agentruntime.Hostname(agentruntime.Hermes, id)
	dashboardHost := dashboardHostname(id)
	configData, err := generateConfig(cfg, primary)
	if err != nil {
		return err
	}
	configData, err = mergePreservedHermesConfigKeys(cfg, id, configData)
	if err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(deploymentDir, valuesFileName), []byte(generateValues(namespace, hostname, dashboardHost, agentBaseURL, token, primary, configData)), 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", valuesFileName, err)
	}
	if err := os.WriteFile(filepath.Join(deploymentDir, helmfileFileName), []byte(generateHelmfile(namespace)), 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", helmfileFileName, err)
	}

	if err := syncRuntimeFiles(cfg, id, configData, u); err != nil {
		return err
	}

	u.Successf("Prepared Hermes runtime config (%d model(s), default: %s)", len(models), primary)
	return nil
}

func generateHelmfile(namespace string) string {
	return fmt.Sprintf(`# Managed by obol agent

repositories:
  - name: obol
    url: https://obolnetwork.github.io/helm-charts/
  - name: bedag
    url: https://bedag.github.io/helm-charts/

releases:
  - name: hermes
    namespace: %s
    createNamespace: true
    chart: bedag/raw
    version: %s
    values:
      - %s

  - name: remote-signer
    namespace: %s
    chart: obol/remote-signer
    version: %s
    values:
      - values-remote-signer.yaml
`, namespace, rawChartVersion, valuesFileName, namespace, agentruntime.RemoteSignerChartVersion)
}

func dashboardHostname(id string) string {
	return agentruntime.DashboardHostname(agentruntime.Hermes, id)
}

func generateValues(namespace, hostname, dashboardHostname, agentBaseURL, token, primary string, configData []byte) string {
	desc := agentruntime.Describe(agentruntime.Hermes)
	configChecksum := sha256.Sum256(configData)

	var b strings.Builder
	fmt.Fprintf(&b, `resources:
  - apiVersion: v1
    kind: ServiceAccount
    metadata:
      name: %s
      namespace: %s
      labels:
        app.kubernetes.io/name: %s
        app.kubernetes.io/managed-by: obol
    automountServiceAccountToken: true

  - apiVersion: v1
    kind: Secret
    metadata:
      name: hermes-api-server
      namespace: %s
      labels:
        app.kubernetes.io/name: %s
        app.kubernetes.io/managed-by: obol
    type: Opaque
    stringData:
      API_SERVER_KEY: %s

  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
      labels:
        app.kubernetes.io/name: %s
        app.kubernetes.io/managed-by: obol
    data:
      config.yaml: |
`, desc.ServiceName, namespace, desc.ServiceName, namespace, desc.ServiceName, quoteYAML(token), desc.ConfigMapName, namespace, desc.ServiceName)
	b.WriteString(indentBlock(string(configData), "        "))
	fmt.Fprintf(&b, `
  - apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: %s
      namespace: %s
      labels:
        app.kubernetes.io/name: %s
        app.kubernetes.io/managed-by: obol
    spec:
      accessModes:
        - ReadWriteOnce
      resources:
        requests:
          storage: 5Gi

  - apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: %s
      namespace: %s
      labels:
        app.kubernetes.io/name: %s
        app.kubernetes.io/managed-by: obol
    spec:
      replicas: 1
      strategy:
        # Hermes is a singleton backed by a ReadWriteOnce PVC. The default
        # RollingUpdate surges a second pod that cannot mount the in-use RWO
        # volume, deadlocking every redeploy/re-sync. Recreate terminates the
        # old pod before starting the new one so the volume is free.
        type: Recreate
        # NOTE: a deployment first created with the default RollingUpdate
        # strategy carries a k8s-populated strategy.rollingUpdate that helm's
        # upgrade does NOT clear when only type flips to Recreate, so the API
        # rejects the update (rollingUpdate may not be specified when strategy
        # type is Recreate) and obol model setup/prefer/sync then silently fail
        # to apply the new model to the running agent. Emitting rollingUpdate:
        # null here does not survive bedag/raw + helm's null handling, so the
        # stale field is cleared imperatively in migrateDeploymentStrategy
        # before each helmfile sync.
      selector:
        matchLabels:
          app.kubernetes.io/name: %s
      template:
        metadata:
          labels:
            app.kubernetes.io/name: %s
            app.kubernetes.io/managed-by: obol
          annotations:
            checksum/hermes-config: "%x"
        spec:
          serviceAccountName: %s
          automountServiceAccountToken: true
          securityContext:
            runAsNonRoot: true
            runAsUser: %d
            runAsGroup: %d
            fsGroup: %d
            fsGroupChangePolicy: Always
          initContainers:
            - name: init-hermes-data
              image: %s
              imagePullPolicy: IfNotPresent
              command:
                - sh
                - -ec
                - |
                  mkdir -p /data/.hermes/home /data/.hermes/workspace /data/.hermes/logs
                  if [ ! -x /opt/hermes/.venv/bin/hermes ]; then
                    echo "Hermes binary missing from image: /opt/hermes/.venv/bin/hermes" >&2
                    exit 1
                  fi
                  if ! /opt/hermes/.venv/bin/python3 -c "import fastapi, uvicorn, telegram, mcp, ptyprocess, simple_term_menu, googleapiclient" >/dev/null 2>&1; then
                    echo "Hermes image is missing required extras: web,messaging,mcp,pty,cli,acp,google" >&2
                    exit 1
                  fi
                  if [ -f /data/.hermes/state.db ]; then
                    if ! /opt/hermes/.venv/bin/python3 - <<'PY'
                  import sqlite3
                  conn = sqlite3.connect('/data/.hermes/state.db')
                  row = conn.execute('PRAGMA quick_check').fetchone()
                  raise SystemExit(0 if row and row[0] == 'ok' else 1)
                  PY
                    then
                      ts="$(date -u +%%Y%%m%%dT%%H%%M%%SZ)"
                      backup_dir="/data/.hermes/backups/state-db-corrupt-$ts"
                      mkdir -p "$backup_dir"
                      cp -a /data/.hermes/state.db* "$backup_dir"/ 2>/dev/null || true
                      mv /data/.hermes/state.db "/data/.hermes/state.db.corrupt-$ts"
                      rm -f /data/.hermes/state.db-shm /data/.hermes/state.db-wal
                      echo "Backed up malformed Hermes state DB to $backup_dir"
                    fi
                  fi
              volumeMounts:
                - name: data
                  mountPath: /data
          containers:
            - name: %s
              image: %s
              imagePullPolicy: IfNotPresent
              command:
                - %s
              args:
                - gateway
                - run
                - --replace
              ports:
                - name: http
                  containerPort: %d
              env:
                - name: HERMES_HOME
                  value: /data/.hermes
                - name: HOME
                  value: /data/.hermes/home
                - name: API_SERVER_ENABLED
                  value: "true"
                - name: API_SERVER_HOST
                  value: "0.0.0.0"
                - name: API_SERVER_PORT
                  value: "%d"
                - name: API_SERVER_KEY
                  valueFrom:
                    secretKeyRef:
                      name: hermes-api-server
                      key: API_SERVER_KEY
                - name: API_SERVER_MODEL_NAME
                  value: %s
                - name: REMOTE_SIGNER_URL
                  value: http://remote-signer:9000
                - name: AGENT_NAMESPACE
                  value: %s
                - name: OBOL_SKILLS_DIR
                  value: /data/.hermes/%s
	`, desc.DataPVCName, namespace, desc.ServiceName, desc.ServiceName, namespace, desc.ServiceName, desc.ServiceName, desc.ServiceName, configChecksum, desc.ServiceName, containerUID, containerGID, containerGID, quoteYAML(image()), desc.ServiceName, quoteYAML(image()), quoteYAML(hermesBinary), desc.DefaultPort, desc.DefaultPort, quoteYAML(primary), quoteYAML(namespace), obolSkillsDirName)

	if agentBaseURL != "" {
		fmt.Fprintf(&b, "                - name: AGENT_BASE_URL\n                  value: %s\n", quoteYAML(agentBaseURL))
	}

	fmt.Fprintf(&b, `              readinessProbe:
                httpGet:
                  path: /health
                  port: %d
                initialDelaySeconds: 5
                periodSeconds: 10
              livenessProbe:
                httpGet:
                  path: /health
                  port: %d
                initialDelaySeconds: 15
                periodSeconds: 20
              startupProbe:
                httpGet:
                  path: /health
                  port: %d
                periodSeconds: 5
                failureThreshold: 24
              volumeMounts:
                - name: data
                  mountPath: /data
            - name: hermes-dashboard
              image: %s
              imagePullPolicy: IfNotPresent
              command:
                - %s
              args:
                - dashboard
                - --host
                - 0.0.0.0
                - --port
                - "%d"
                - --no-open
                - --insecure
              ports:
                - name: dashboard
                  containerPort: %d
              env:
                - name: HERMES_HOME
                  value: /data/.hermes
                - name: HOME
                  value: /data/.hermes/home
                - name: GATEWAY_HEALTH_URL
                  value: http://localhost:%d
                - name: GATEWAY_HEALTH_TIMEOUT
                  value: "3"
                # Local k3d/dev clusters do not expose the dashboard's messaging integrations
                # (Telegram/Discord/etc.) to the public internet, so enabling open-allowlist
                # is safe here. Production deployments must override this via a values overlay.
                - name: GATEWAY_ALLOW_ALL_USERS
                  value: "true"
                # v2026.7.x hardening: a non-loopback (0.0.0.0) dashboard bind now
                # requires an auth provider; the legacy --insecure flag no longer
                # bypasses it (closes the hermes-0day unauthenticated-dashboard
                # hole). Register the bundled basic-auth provider using the agent's
                # existing API token as the password (already surfaced to the
                # operator). Probe path /api/status stays auth-exempt.
                - name: HERMES_DASHBOARD_BASIC_AUTH_USERNAME
                  value: obol
                - name: HERMES_DASHBOARD_BASIC_AUTH_PASSWORD
                  value: %s
              readinessProbe:
                httpGet:
                  path: /api/status
                  port: %d
                initialDelaySeconds: 5
                periodSeconds: 10
              livenessProbe:
                httpGet:
                  path: /api/status
                  port: %d
                initialDelaySeconds: 15
                periodSeconds: 20
              startupProbe:
                httpGet:
                  path: /api/status
                  port: %d
                periodSeconds: 5
                failureThreshold: 24
              volumeMounts:
                - name: data
                  mountPath: /data
          volumes:
            - name: data
              persistentVolumeClaim:
                claimName: %s

  - apiVersion: v1
    kind: Service
    metadata:
      name: %s
      namespace: %s
      labels:
        app.kubernetes.io/name: %s
        app.kubernetes.io/managed-by: obol
    spec:
      selector:
        app.kubernetes.io/name: %s
      ports:
        - name: http
          port: %d
          targetPort: http
        - name: dashboard
          port: %d
          targetPort: dashboard

  - apiVersion: gateway.networking.k8s.io/v1
    kind: HTTPRoute
    metadata:
      name: %s
      namespace: %s
    spec:
      hostnames:
        - %s
      parentRefs:
        - name: traefik-gateway
          namespace: traefik
          sectionName: web
      rules:
        - backendRefs:
            - name: %s
              port: %d

  - apiVersion: gateway.networking.k8s.io/v1
    kind: HTTPRoute
    metadata:
      name: %s-dashboard
      namespace: %s
    spec:
      hostnames:
        - %s
      parentRefs:
        - name: traefik-gateway
          namespace: traefik
          sectionName: web
      rules:
        - backendRefs:
            - name: %s
              port: %d
`, desc.DefaultPort, desc.DefaultPort, desc.DefaultPort,
		quoteYAML(image()), quoteYAML(hermesBinary), dashboardPort, dashboardPort, desc.DefaultPort, quoteYAML(token), dashboardPort, dashboardPort, dashboardPort,
		desc.DataPVCName,
		desc.ServiceName, namespace, desc.ServiceName, desc.ServiceName, desc.DefaultPort, dashboardPort,
		desc.ServiceName, namespace, quoteYAML(hostname), desc.ServiceName, desc.DefaultPort,
		desc.ServiceName, namespace, quoteYAML(dashboardHostname), desc.ServiceName, dashboardPort)

	return strings.ReplaceAll(b.String(), "\t", "")
}

func syncRuntimeFiles(cfg *config.Config, id string, configData []byte, u *ui.UI) error {
	targetDir := agentruntime.HomePath(cfg, agentruntime.Hermes, id)
	ensureVolumeWritableFn(cfg, targetDir, u)
	defer fixRuntimeVolumeOwnershipFn(cfg, targetDir, u)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("failed to create Hermes home %s: %w", targetDir, err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "config.yaml"), configData, 0o600); err != nil {
		return fmt.Errorf("failed to write Hermes config: %w", err)
	}
	if err := syncObolSkills(cfg, id); err != nil {
		return err
	}
	if err := removeLegacyHeartbeat(targetDir); err != nil {
		return err
	}
	return nil
}

func removeLegacyHeartbeat(hermesHome string) error {
	heartbeatPath := filepath.Join(hermesHome, "workspace", "HEARTBEAT.md")
	if err := os.Remove(heartbeatPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove legacy heartbeat: %w", err)
	}
	return nil
}

func syncObolSkills(cfg *config.Config, id string) error {
	targetDir := filepath.Join(agentruntime.HomePath(cfg, agentruntime.Hermes, id), obolSkillsDirName)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("failed to create Obol skills directory: %w", err)
	}
	if err := obolembed.CopySkills(targetDir); err != nil {
		return fmt.Errorf("failed to copy Obol skills: %w", err)
	}
	return nil
}

// configuredModels returns the agent-facing model list and the primary model
// name. Both are returned as round-trippable LiteLLM `model_name` strings:
// the agent passes `primary` back as the `model` field on chat-completion
// calls, and LiteLLM matches by exact string against the entries in the
// returned slice. NO provider-prefix stripping happens on this path —
// LiteLLM `model_name` is the contract identifier end-to-end.
//
// See internal/model/model.go (AddCustomEndpoint, buildModelEntries) for
// where the bare-name convention is written into the LiteLLM ConfigMap.
func configuredModels(cfg *config.Config, u *ui.UI) ([]string, string, error) {
	models, err := model.GetConfiguredModels(cfg)
	if err == nil && len(models) > 0 {
		primary, _ := rankModels(models)
		return models, primary, nil
	}

	ollamaModels, ollamaErr := model.ListOllamaModels()
	if ollamaErr != nil || len(ollamaModels) == 0 {
		return nil, "", errors.New("no LiteLLM models configured")
	}

	names := model.AutoConfigOllamaModelNames(ollamaModels)
	if len(names) == 0 {
		return nil, "", errors.New("no LiteLLM models configured")
	}

	// Skip LiteLLM auto-config when the cluster isn't reachable — stack-up's
	// own auto-config step runs after deploy. Lets `obol hermes onboard
	// --no-sync` scaffold the deploy dir without a live cluster.
	if _, statErr := os.Stat(filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")); statErr == nil {
		if err := model.ConfigureLiteLLM(cfg, u, "ollama", "", names); err != nil {
			return nil, "", fmt.Errorf("failed to auto-configure LiteLLM for Ollama: %w", err)
		}
	}

	primary, _ := rankModels(names)
	return names, primary, nil
}

func generateConfig(cfg *config.Config, primary string) ([]byte, error) {
	payload := map[string]any{
		"model": map[string]any{
			"default":  primary,
			"provider": "custom",
			"base_url": "http://litellm.llm.svc.cluster.local:4000/v1",
			"api_key":  litellmMasterKey(cfg),
		},
		"terminal": map[string]any{
			"backend": "local",
			"cwd":     "/data/.hermes/workspace",
			// pay-agent streams up to 1h; chat buys must not die at 180s.
			"timeout":                       3600,
			"lifetime_seconds":              3700,
			"docker_mount_cwd_to_workspace": false,
		},
		// Tirith blocks .dev seller URLs in pay-agent shell commands unless
		// explicitly allowed. Stack-managed buyers need this for tunnel hosts.
		"command_allowlist": []string{"tirith:lookalike_tld"},
		"skills": map[string]any{
			"external_dirs": []string{"/data/.hermes/" + obolSkillsDirName},
		},
	}
	return yaml.Marshal(payload)
}

// mergePreservedHermesConfigKeys carries operator-edited Hermes keys across
// obol agent sync. generateConfig only knows stack-managed defaults; security
// knobs like command_allowlist set via `hermes config` must survive re-render.
func mergePreservedHermesConfigKeys(cfg *config.Config, id string, generated []byte) ([]byte, error) {
	path := filepath.Join(agentruntime.HomePath(cfg, agentruntime.Hermes, id), "config.yaml")
	existingData, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return generated, nil
		}
		return nil, fmt.Errorf("read existing Hermes config %s: %w", path, err)
	}

	var gen map[string]any
	var exist map[string]any
	if err := yaml.Unmarshal(generated, &gen); err != nil {
		return nil, fmt.Errorf("parse generated Hermes config: %w", err)
	}
	if err := yaml.Unmarshal(existingData, &exist); err != nil {
		return generated, nil
	}

	gen["command_allowlist"] = mergeCommandAllowlist(
		stringSliceFromConfig(gen["command_allowlist"]),
		stringSliceFromConfig(exist["command_allowlist"]),
	)

	out, err := yaml.Marshal(gen)
	if err != nil {
		return nil, fmt.Errorf("marshal merged Hermes config: %w", err)
	}
	return out, nil
}

func mergeCommandAllowlist(generated, existing []string) []string {
	// No capacity hints: the command_allowlist is an operator config list of a
	// handful of entries, so preallocation is noise — and summing the two
	// lengths trips CodeQL's allocation-size-overflow heuristic (a false
	// positive here, but not worth a standing dismissal). Grow on demand.
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, list := range [][]string{generated, existing} {
		for _, entry := range list {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if _, ok := seen[entry]; ok {
				continue
			}
			seen[entry] = struct{}{}
			out = append(out, entry)
		}
	}
	return out
}

func stringSliceFromConfig(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		return []string{s}
	default:
		return nil
	}
}

func currentAgentBaseURL(deploymentDir string) string {
	raw, err := os.ReadFile(filepath.Join(deploymentDir, valuesFileName))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if strings.Contains(line, "name: AGENT_BASE_URL") {
			if i+1 < len(lines) && strings.Contains(lines[i+1], "value:") {
				return strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i+1]), "value:")), `"'`)
			}
		}
	}
	return ""
}

func gatewayTokenPath(deploymentDir string) string {
	return filepath.Join(deploymentDir, gatewayTokenFileName)
}

func ensureGatewayToken(deploymentDir string) (string, error) {
	if data, err := os.ReadFile(gatewayTokenPath(deploymentDir)); err == nil {
		token := strings.TrimSpace(string(data))
		if token != "" {
			return token, nil
		}
	}

	token, err := generateGatewayToken()
	if err != nil {
		return "", err
	}
	if err := persistGatewayToken(deploymentDir, token); err != nil {
		return "", err
	}
	return token, nil
}

func persistGatewayToken(deploymentDir, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("gateway token is empty")
	}
	return os.WriteFile(gatewayTokenPath(deploymentDir), []byte(token+"\n"), 0o600)
}

func generateGatewayToken() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate gateway token: %w", err)
	}

	out := make([]byte, 32)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

func image() string {
	if override := strings.TrimSpace(os.Getenv("OBOL_HERMES_IMAGE")); override != "" {
		return override
	}
	return defaultImage
}

// ImageRef returns the upstream Hermes container image ref. Used by
// stack.devPreloadImages so `obol stack up` preloads the image through the
// k3s node's CRI before the first Hermes pod starts — otherwise the first pod
// stalls waiting for the registry mirror to serve a cold pull, which has
// caused flow-14 step 32 timeouts in the past.
func ImageRef() string {
	return image()
}

func quoteYAML(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func indentBlock(value, prefix string) string {
	if value == "" {
		return prefix + "\n"
	}

	lines := strings.Split(strings.TrimRight(value, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n") + "\n"
}

func refreshHelmRepos(cfg *config.Config) error {
	helmBinary := filepath.Join(cfg.BinDir, "helm")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	env := append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	addObolCmd := exec.Command(helmBinary, "repo", "add", "obol", "https://obolnetwork.github.io/helm-charts/")
	addObolCmd.Env = env
	addObolOut, addObolErr := addObolCmd.CombinedOutput()
	if addObolErr != nil && !strings.Contains(string(addObolOut), `"obol" already exists`) {
		return fmt.Errorf("helm repo add obol failed: %w\n%s", addObolErr, string(addObolOut))
	}

	addBedagCmd := exec.Command(helmBinary, "repo", "add", "bedag", "https://bedag.github.io/helm-charts/")
	addBedagCmd.Env = env
	addBedagOut, addBedagErr := addBedagCmd.CombinedOutput()
	if addBedagErr != nil && !strings.Contains(string(addBedagOut), `"bedag" already exists`) {
		return fmt.Errorf("helm repo add bedag failed: %w\n%s", addBedagErr, string(addBedagOut))
	}

	updateCmd := exec.Command(helmBinary, "repo", "update", "obol", "bedag")
	updateCmd.Env = env
	updateOut, updateErr := updateCmd.CombinedOutput()
	if updateErr != nil {
		return fmt.Errorf("helm repo update failed: %w\n%s", updateErr, string(updateOut))
	}

	return nil
}

func litellmMasterKey(cfg *config.Config) string {
	stackIDPath := filepath.Join(cfg.ConfigDir, ".stack-id")
	data, err := os.ReadFile(stackIDPath)
	if err != nil {
		return "sk-obol-default"
	}
	return "sk-obol-" + strings.TrimSpace(string(data))
}

// rankModels delegates to model.Rank, which preserves configured LiteLLM model
// order and keeps known embedding-only models behind chat-capable models. Kept
// as a thin wrapper so call sites don't need to import internal/model directly.
//
// IMPORTANT: do NOT strip provider prefixes here. model.Rank returns the
// original strings so the agent can round-trip them back to LiteLLM. Stripping
// at this layer would break that round-trip — that's exactly the double-strip
// bug that ca820c9 worked around for custom endpoints.
func rankModels(models []string) (primary string, fallbacks []string) {
	return model.Rank(models)
}

func k3dNodeExec(cfg *config.Config, hostPath, shellCmd string) error {
	_, err := k3dNodeExecOutput(cfg, hostPath, shellCmd)
	return err
}

func k3dNodeExecOutput(cfg *config.Config, hostPath, shellCmd string) ([]byte, error) {
	stackID := ""
	if data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, ".stack-id")); err == nil {
		stackID = strings.TrimSpace(string(data))
	}
	if stackID == "" {
		return nil, fmt.Errorf("stack ID not found")
	}

	container := fmt.Sprintf("k3d-obol-stack-%s-server-0", stackID)
	relPath, err := filepath.Rel(cfg.DataDir, hostPath)
	if err != nil {
		return nil, fmt.Errorf("cannot compute relative path from %s to %s: %w", cfg.DataDir, hostPath, err)
	}
	if strings.HasPrefix(relPath, "..") {
		return nil, fmt.Errorf("path %s is not under DataDir %s", hostPath, cfg.DataDir)
	}

	nodePath := filepath.Join("/data", relPath)
	quoted := "'" + strings.ReplaceAll(nodePath, "'", "'\"'\"'") + "'"
	expanded := strings.ReplaceAll(shellCmd, "{}", quoted)

	cmd := exec.Command("docker", "exec", container, "sh", "-c", expanded)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker exec %s: %w: %s", container, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func ensureVolumeWritable(cfg *config.Config, hostPath string, u *ui.UI) {
	backendName := "k3d"
	if data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, ".stack-backend")); err == nil {
		backendName = strings.TrimSpace(string(data))
	}

	if backendName != "k3d" {
		return
	}

	uid := os.Getuid()
	gid := os.Getgid()
	shellCmd := fmt.Sprintf("mkdir -p {} && chown -R %d:%d {}", uid, gid)

	if err := k3dNodeExec(cfg, hostPath, shellCmd); err != nil && u != nil {
		u.Warnf("Could not pre-create volume directory %s: %v", hostPath, err)
	}
}

func fixRuntimeVolumeOwnership(cfg *config.Config, hostPath string, u *ui.UI) {
	backendName := "k3d"
	if data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, ".stack-backend")); err == nil {
		backendName = strings.TrimSpace(string(data))
	}

	owner := fmt.Sprintf("%d:%d", containerUID, containerGID)
	switch backendName {
	case "k3d":
		if err := k3dNodeExec(cfg, hostPath, "chown -R "+owner+" {}"); err != nil && u != nil {
			u.Warnf("Failed to fix volume ownership for %s: %v", hostPath, err)
		}
	default:
		_ = os.Chown(hostPath, containerUID, containerGID)
	}
}
