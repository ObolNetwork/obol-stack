package appkit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// DeleteDeployment removes a deployment's namespace and local config directory.
// If force is false, prompts the user for confirmation.
func DeleteDeployment(cfg *config.Config, appName, id string, force bool) error {
	namespace := Namespace(appName, id)
	paths := ResolveDeployment(cfg, appName, id)

	fmt.Printf("Deleting %s: %s/%s\n", appName, appName, id)
	fmt.Printf("Namespace: %s\n", namespace)

	configExists := false
	if _, err := os.Stat(paths.DeploymentDir); err == nil {
		configExists = true
	}

	namespaceExists := false
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); err == nil {
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "get", "namespace", namespace)
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
		if err := cmd.Run(); err == nil {
			namespaceExists = true
		}
	}

	if !namespaceExists && !configExists {
		return fmt.Errorf("instance not found: %s", id)
	}

	fmt.Println("\nResources to be deleted:")
	if namespaceExists {
		fmt.Printf("  [x] Kubernetes namespace: %s\n", namespace)
	} else {
		fmt.Printf("  [ ] Kubernetes namespace: %s (not found)\n", namespace)
	}
	if configExists {
		fmt.Printf("  [x] Configuration: %s\n", paths.DeploymentDir)
	}

	if !force {
		fmt.Print("\nProceed with deletion? [y/N]: ")
		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			fmt.Println("Deletion cancelled")
			return nil
		}
	}

	if namespaceExists {
		fmt.Printf("\nDeleting namespace %s...\n", namespace)
		kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
		cmd := exec.Command(kubectlBinary, "delete", "namespace", namespace,
			"--force", "--grace-period=0")
		cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to delete namespace: %w", err)
		}
		fmt.Println("Namespace deleted")
	}

	if configExists {
		fmt.Printf("Deleting configuration...\n")
		if err := os.RemoveAll(paths.DeploymentDir); err != nil {
			return fmt.Errorf("failed to delete config directory: %w", err)
		}
		fmt.Println("Configuration deleted")

		parentDir := filepath.Join(cfg.ConfigDir, "applications", appName)
		entries, err := os.ReadDir(parentDir)
		if err == nil && len(entries) == 0 {
			os.Remove(parentDir)
		}
	}

	fmt.Printf("\n%s %s deleted successfully!\n", appName, id)
	return nil
}

// ListDeployments displays installed instances for a given app.
func ListDeployments(cfg *config.Config, appName, domain string) error {
	appsDir := filepath.Join(cfg.ConfigDir, "applications", appName)

	if _, err := os.Stat(appsDir); os.IsNotExist(err) {
		fmt.Printf("No %s instances installed\n", appName)
		fmt.Printf("\nTo create one: obol %s up\n", appName)
		return nil
	}

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	if len(entries) == 0 {
		fmt.Printf("No %s instances installed\n", appName)
		return nil
	}

	displayName := strings.ToUpper(appName[:1]) + appName[1:]
	fmt.Printf("%s instances:\n\n", displayName)

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		namespace := Namespace(appName, id)
		hostname := Hostname(appName, id, domain)
		fmt.Printf("  %s\n", id)
		fmt.Printf("    Namespace: %s\n", namespace)
		fmt.Printf("    URL:       http://%s\n", hostname)
		fmt.Println()
		count++
	}

	fmt.Printf("Total: %d instance(s)\n", count)
	return nil
}

// GetSecretValue retrieves a specific key from a Kubernetes Secret matching
// a label selector in the given namespace.
func GetSecretValue(cfg *config.Config, namespace, labelSelector, key string) (string, error) {
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return "", fmt.Errorf("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	cmd := exec.Command(kubectlBinary, "get", "secret", "-n", namespace,
		"-l", labelSelector,
		"-o", "json")
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to get secret: %w\n%s", err, stderr.String())
	}

	var secretList struct {
		Items []struct {
			Data map[string]string `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &secretList); err != nil {
		return "", fmt.Errorf("failed to parse secret: %w", err)
	}

	if len(secretList.Items) == 0 {
		return "", fmt.Errorf("no secrets found in namespace %s", namespace)
	}

	for _, item := range secretList.Items {
		if encoded, ok := item.Data[key]; ok {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return "", fmt.Errorf("failed to decode secret value: %w", err)
			}
			return string(decoded), nil
		}
	}

	return "", fmt.Errorf("%s not found in namespace %s secrets", key, namespace)
}
