package tee

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CoCo constants for Confidential Containers operator installation.
const (
	CoCoChartOCI     = "oci://ghcr.io/confidential-containers/charts/confidential-containers"
	CoCoChartVersion = "0.18.0"
	CoCoNamespace    = "coco-system"
	CoCoReleaseName  = "coco"
)

// CoCoRuntimeClass represents a CoCo-provided Kubernetes RuntimeClass.
type CoCoRuntimeClass string

const (
	// RuntimeQEMUCoCoDev is the development runtime (QEMU, no TEE hardware).
	// Use for testing on any machine with /dev/kvm.
	RuntimeQEMUCoCoDev CoCoRuntimeClass = "kata-qemu-coco-dev"

	// RuntimeQEMUSNP is for AMD SEV-SNP confidential VMs.
	RuntimeQEMUSNP CoCoRuntimeClass = "kata-qemu-snp"

	// RuntimeQEMUTDX is for Intel TDX confidential VMs.
	RuntimeQEMUTDX CoCoRuntimeClass = "kata-qemu-tdx"
)

// ValidCoCoRuntimes returns the set of CoCo runtime classes this package
// knows about.
func ValidCoCoRuntimes() []CoCoRuntimeClass {
	return []CoCoRuntimeClass{
		RuntimeQEMUCoCoDev,
		RuntimeQEMUSNP,
		RuntimeQEMUTDX,
	}
}

// ParseCoCoRuntime validates a string as a known CoCo runtime class.
// "none" returns an empty string (no CoCo).
func ParseCoCoRuntime(s string) (CoCoRuntimeClass, error) {
	if s == "" || s == "none" {
		return "", nil
	}

	switch CoCoRuntimeClass(s) {
	case RuntimeQEMUCoCoDev, RuntimeQEMUSNP, RuntimeQEMUTDX:
		return CoCoRuntimeClass(s), nil
	default:
		return "", fmt.Errorf("tee: unknown CoCo runtime %q (valid: %s)",
			s, strings.Join(cocoRuntimeStrings(), ", "))
	}
}

func cocoRuntimeStrings() []string {
	runtimes := ValidCoCoRuntimes()

	ss := make([]string, len(runtimes))
	for i, r := range runtimes {
		ss[i] = string(r)
	}

	return ss
}

// CoCoStatus describes the installation state of the CoCo operator.
type CoCoStatus struct {
	Installed      bool     `json:"installed"`
	Version        string   `json:"version,omitempty"`
	Namespace      string   `json:"namespace,omitempty"`
	RuntimeClasses []string `json:"runtime_classes,omitempty"`
	OperatorReady  bool     `json:"operator_ready"`
	KVMAvailable   bool     `json:"kvm_available"`
}

// CoCoInstallOpts configures the CoCo Helm install.
type CoCoInstallOpts struct {
	// HelmBin is the path to the helm binary (default: "helm").
	HelmBin string

	// KubectlBin is the path to kubectl (default: "kubectl").
	KubectlBin string

	// Kubeconfig is the path to the kubeconfig file.
	// Empty string uses the default kube config.
	Kubeconfig string

	// DryRun only prints the commands without executing them.
	DryRun bool
}

func (o *CoCoInstallOpts) helm() string {
	if o != nil && o.HelmBin != "" {
		return o.HelmBin
	}

	return "helm"
}

func (o *CoCoInstallOpts) kubectl() string {
	if o != nil && o.KubectlBin != "" {
		return o.KubectlBin
	}

	return "kubectl"
}

func (o *CoCoInstallOpts) kubeconfigArgs() []string {
	if o != nil && o.Kubeconfig != "" {
		return []string{"--kubeconfig", o.Kubeconfig}
	}

	return nil
}

// InstallCoCo installs the Confidential Containers operator on a k3s cluster.
//
// This runs:
//
//	helm install coco oci://ghcr.io/confidential-containers/charts/confidential-containers \
//	  --version 0.18.0 \
//	  --set kata-as-coco-runtime.k8sDistribution=k3s \
//	  --namespace coco-system --create-namespace
//
// The function returns the helm install command output or an error.
func InstallCoCo(ctx context.Context, opts *CoCoInstallOpts) (string, error) {
	args := []string{
		"install", CoCoReleaseName,
		CoCoChartOCI,
		"--version", CoCoChartVersion,
		"--set", "kata-as-coco-runtime.k8sDistribution=k3s",
		"--namespace", CoCoNamespace,
		"--create-namespace",
		"--wait",
		"--timeout", "5m",
	}
	args = append(args, opts.kubeconfigArgs()...)

	if opts != nil && opts.DryRun {
		return fmt.Sprintf("%s %s", opts.helm(), strings.Join(args, " ")), nil
	}

	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, opts.helm(), args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tee/coco: helm install failed: %w\n%s", err, string(out))
	}

	return string(out), nil
}

// UninstallCoCo removes the CoCo operator.
func UninstallCoCo(ctx context.Context, opts *CoCoInstallOpts) (string, error) {
	args := []string{
		"uninstall", CoCoReleaseName,
		"--namespace", CoCoNamespace,
	}
	args = append(args, opts.kubeconfigArgs()...)

	if opts != nil && opts.DryRun {
		return fmt.Sprintf("%s %s", opts.helm(), strings.Join(args, " ")), nil
	}

	cmd := exec.CommandContext(ctx, opts.helm(), args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tee/coco: helm uninstall failed: %w\n%s", err, string(out))
	}

	return string(out), nil
}

// CheckCoCo queries the cluster for CoCo installation status.
func CheckCoCo(ctx context.Context, opts *CoCoInstallOpts) (*CoCoStatus, error) {
	status := &CoCoStatus{
		Namespace: CoCoNamespace,
	}

	// Check if KVM is available on the host.
	status.KVMAvailable = checkKVM()

	// Check helm release status.
	helmArgs := []string{
		"status", CoCoReleaseName,
		"--namespace", CoCoNamespace,
		"--output", "json",
	}
	helmArgs = append(helmArgs, opts.kubeconfigArgs()...)

	cmd := exec.CommandContext(ctx, opts.helm(), helmArgs...)

	helmOut, err := cmd.CombinedOutput()
	if err != nil {
		// Not installed or helm error.
		status.Installed = false
		return status, nil
	}

	// Parse helm status JSON.
	var helmStatus struct {
		Info struct {
			Status string `json:"status"`
		} `json:"info"`
		Version int `json:"version"`
	}
	if json.Unmarshal(helmOut, &helmStatus) == nil {
		status.Installed = helmStatus.Info.Status == "deployed"
		status.Version = CoCoChartVersion
		status.OperatorReady = helmStatus.Info.Status == "deployed"
	}

	// List RuntimeClasses from cluster.
	rcs, err := listRuntimeClasses(ctx, opts)
	if err == nil {
		status.RuntimeClasses = rcs
	}

	return status, nil
}

// listRuntimeClasses queries the cluster for Kata/CoCo RuntimeClasses.
func listRuntimeClasses(ctx context.Context, opts *CoCoInstallOpts) ([]string, error) {
	args := []string{
		"get", "runtimeclasses",
		"-o", "jsonpath={.items[*].metadata.name}",
	}
	args = append(args, opts.kubeconfigArgs()...)

	cmd := exec.CommandContext(ctx, opts.kubectl(), args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tee/coco: list runtimeclasses: %w", err)
	}

	names := strings.Fields(strings.TrimSpace(string(out)))
	// Filter to only CoCo/Kata runtimes.
	var result []string

	for _, name := range names {
		if strings.HasPrefix(name, "kata-") {
			result = append(result, name)
		}
	}

	return result, nil
}

// checkKVM checks if /dev/kvm exists (required for CoCo QEMU runtimes).
func checkKVM() bool {
	_, err := exec.LookPath("ls")
	if err != nil {
		return false
	}

	cmd := exec.Command("test", "-c", "/dev/kvm")

	return cmd.Run() == nil
}
