package tunnel

import (
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func applyManagementModeConfigMap(cfg *config.Config, u *ui.UI, kubeconfigPath, mode, transportProtocol string) error {
	protocol := normalizeTunnelTransportProtocol(transportProtocol)
	if protocol == "" {
		protocol = tunnelTransportAuto
	}

	manifest := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  %s: %q
  %s: %q
`, managementConfigMapName, tunnelNamespace, managementConfigModeKey, mode, managementConfigProtocolKey, protocol)

	return kubectlApply(cfg, u, kubeconfigPath, []byte(manifest))
}

func deleteRemoteManagedK8sResources(cfg *config.Config, u *ui.UI, kubeconfigPath string) error {
	return kubectlDelete(cfg, u, kubeconfigPath, "secret", tunnelTokenSecretName)
}

func deleteLocalManagedK8sResources(cfg *config.Config, u *ui.UI, kubeconfigPath string) error {
	if err := kubectlDelete(cfg, u, kubeconfigPath, "secret", localManagedSecretName); err != nil {
		return err
	}

	return kubectlDelete(cfg, u, kubeconfigPath, "configmap", localManagedConfigMapName)
}

func kubectlDelete(cfg *config.Config, u *ui.UI, kubeconfigPath, kind, name string) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	cmd := exec.Command(kubectlPath,
		"--kubeconfig", kubeconfigPath,
		"delete", kind, name,
		"-n", tunnelNamespace,
		"--ignore-not-found",
	)

	if err := u.Exec(ui.ExecConfig{
		Name: fmt.Sprintf("Deleting %s/%s", kind, name),
		Cmd:  cmd,
	}); err != nil {
		return fmt.Errorf("kubectl delete %s/%s failed: %w", kind, name, err)
	}

	return nil
}
