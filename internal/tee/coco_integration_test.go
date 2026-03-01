//go:build integration

// Package tee integration tests require a running k3s cluster with the
// Confidential Containers (CoCo) operator installed. Run them with:
//
//	go test -tags integration -v -count=1 ./internal/tee/ -run TestIntegration
//
// Prerequisites:
//   - k3s cluster running (obol stack up)
//   - CoCo operator installed (helm install coco ...)
//   - /dev/kvm available on worker nodes
//   - KUBECONFIG set or default ~/.kube/config valid
//
// These tests exercise the CoCo QEMU dev runtime (kata-qemu-coco-dev)
// which does not require real TEE hardware — it uses a QEMU VM with a
// minimal kernel to provide the same pod isolation boundary.

package tee

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// kubeconfig returns the path to kubeconfig, checking KUBECONFIG env first.
func kubeconfig() string {
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		return kc
	}
	home, _ := os.UserHomeDir()
	return home + "/.kube/config"
}

func kubectl(args ...string) *exec.Cmd {
	kb := os.Getenv("KUBECTL_BIN")
	if kb == "" {
		kb = "kubectl"
	}
	allArgs := append([]string{"--kubeconfig", kubeconfig()}, args...)
	return exec.Command(kb, allArgs...)
}

// TestIntegration_CoCoOperatorInstalled verifies the CoCo Helm release is
// deployed and the operator pods are running.
func TestIntegration_CoCoOperatorInstalled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	status, err := CheckCoCo(ctx, &CoCoInstallOpts{
		Kubeconfig: kubeconfig(),
	})
	if err != nil {
		t.Fatalf("CheckCoCo failed: %v", err)
	}

	if !status.Installed {
		t.Fatal("CoCo operator is not installed — run: helm install coco " + CoCoChartOCI +
			" --version " + CoCoChartVersion +
			" --set kata-as-coco-runtime.k8sDistribution=k3s" +
			" --namespace " + CoCoNamespace + " --create-namespace")
	}

	if !status.OperatorReady {
		t.Error("CoCo operator is installed but not in 'deployed' state")
	}

	t.Logf("CoCo status: installed=%v operator_ready=%v version=%s kvm=%v runtimes=%v",
		status.Installed, status.OperatorReady, status.Version,
		status.KVMAvailable, status.RuntimeClasses)
}

// TestIntegration_RuntimeClassExists verifies kata-qemu-coco-dev RuntimeClass
// is registered in the cluster.
func TestIntegration_RuntimeClassExists(t *testing.T) {
	cmd := kubectl("get", "runtimeclass", string(RuntimeQEMUCoCoDev), "-o", "name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("RuntimeClass %q not found: %v\n%s", RuntimeQEMUCoCoDev, err, out)
	}
	if !strings.Contains(string(out), string(RuntimeQEMUCoCoDev)) {
		t.Errorf("expected RuntimeClass name in output, got: %s", out)
	}
	t.Logf("RuntimeClass %q exists", RuntimeQEMUCoCoDev)
}

// TestIntegration_KVMAvailable verifies /dev/kvm is accessible.
func TestIntegration_KVMAvailable(t *testing.T) {
	if !checkKVM() {
		t.Skip("/dev/kvm not available — CoCo QEMU runtimes require KVM")
	}
	t.Log("/dev/kvm is available")
}

// TestIntegration_CoCoDevPod deploys a minimal pod with kata-qemu-coco-dev
// runtime, verifies it reaches Running state, checks the kernel version
// inside the pod differs from the host, and cleans up.
func TestIntegration_CoCoDevPod(t *testing.T) {
	ns := "coco-test-" + fmt.Sprintf("%d", time.Now().Unix())

	// Create test namespace.
	if out, err := kubectl("create", "namespace", ns).CombinedOutput(); err != nil {
		t.Fatalf("create namespace: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		kubectl("delete", "namespace", ns, "--ignore-not-found").Run()
	})

	// Deploy a minimal pod with CoCo dev runtime.
	podManifest := fmt.Sprintf(`{
		"apiVersion": "v1",
		"kind": "Pod",
		"metadata": {
			"name": "coco-test-pod",
			"namespace": %q
		},
		"spec": {
			"runtimeClassName": %q,
			"containers": [{
				"name": "test",
				"image": "busybox:latest",
				"command": ["sleep", "300"],
				"resources": {
					"limits": {"cpu": "100m", "memory": "64Mi"}
				}
			}],
			"restartPolicy": "Never"
		}
	}`, ns, RuntimeQEMUCoCoDev)

	applyCmd := kubectl("apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(podManifest)
	if out, err := applyCmd.CombinedOutput(); err != nil {
		t.Fatalf("apply pod: %v\n%s", err, out)
	}

	// Wait for pod to be Running (up to 2 minutes for image pull + VM boot).
	t.Log("Waiting for CoCo dev pod to reach Running state...")
	waitCmd := kubectl("wait", "--for=condition=Ready", "pod/coco-test-pod",
		"-n", ns, "--timeout=120s")
	if out, err := waitCmd.CombinedOutput(); err != nil {
		// Get pod status for diagnostics.
		descCmd := kubectl("describe", "pod/coco-test-pod", "-n", ns)
		desc, _ := descCmd.CombinedOutput()
		t.Fatalf("pod not ready: %v\n%s\n--- describe ---\n%s", err, out, desc)
	}

	// Get kernel version inside the pod (should differ from host).
	execCmd := kubectl("exec", "coco-test-pod", "-n", ns, "--",
		"uname", "-r")
	podKernel, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec uname: %v\n%s", err, podKernel)
	}
	podKernelStr := strings.TrimSpace(string(podKernel))

	// Get host kernel version.
	hostKernel, err := exec.Command("uname", "-r").Output()
	if err != nil {
		t.Fatalf("host uname: %v", err)
	}
	hostKernelStr := strings.TrimSpace(string(hostKernel))

	t.Logf("Host kernel:     %s", hostKernelStr)
	t.Logf("Pod kernel:      %s", podKernelStr)

	// The CoCo dev runtime runs a separate QEMU VM with its own kernel,
	// so the kernel versions should differ.
	if podKernelStr == hostKernelStr {
		t.Error("pod kernel matches host kernel — CoCo VM isolation may not be active")
	} else {
		t.Log("Kernel version differs — CoCo QEMU VM isolation confirmed")
	}
}

// TestIntegration_InferenceGatewayInCoCo deploys the inference gateway
// inside a CoCo dev pod and tests the attestation endpoint.
func TestIntegration_InferenceGatewayInCoCo(t *testing.T) {
	ns := "coco-inference-test-" + fmt.Sprintf("%d", time.Now().Unix())

	// Create test namespace.
	if out, err := kubectl("create", "namespace", ns).CombinedOutput(); err != nil {
		t.Fatalf("create namespace: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		kubectl("delete", "namespace", ns, "--ignore-not-found").Run()
	})

	// Deploy the inference gateway with CoCo dev runtime and stub TEE.
	// This simulates the production deployment topology.
	gatewayManifest := fmt.Sprintf(`{
		"apiVersion": "v1",
		"kind": "Pod",
		"metadata": {
			"name": "inference-gw-test",
			"namespace": %q,
			"labels": {"app": "inference-gw-test"}
		},
		"spec": {
			"runtimeClassName": %q,
			"containers": [{
				"name": "gateway",
				"image": "ghcr.io/obolnetwork/inference-gateway:latest",
				"ports": [{"containerPort": 8402}],
				"args": [
					"--listen=:8402",
					"--upstream=http://localhost:11434",
					"--wallet=0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
					"--tee=stub",
					"--model-hash=e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
				],
				"readinessProbe": {
					"httpGet": {"path": "/health", "port": 8402},
					"initialDelaySeconds": 3,
					"periodSeconds": 5
				},
				"resources": {
					"limits": {"cpu": "500m", "memory": "256Mi"}
				}
			}],
			"restartPolicy": "Never"
		}
	}`, ns, RuntimeQEMUCoCoDev)

	applyCmd := kubectl("apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(gatewayManifest)
	if out, err := applyCmd.CombinedOutput(); err != nil {
		t.Fatalf("apply gateway pod: %v\n%s", err, out)
	}

	// Wait for gateway to be ready.
	t.Log("Waiting for inference gateway in CoCo pod to become ready...")
	waitCmd := kubectl("wait", "--for=condition=Ready", "pod/inference-gw-test",
		"-n", ns, "--timeout=180s")
	if out, err := waitCmd.CombinedOutput(); err != nil {
		descCmd := kubectl("describe", "pod/inference-gw-test", "-n", ns)
		desc, _ := descCmd.CombinedOutput()
		t.Fatalf("gateway pod not ready: %v\n%s\n--- describe ---\n%s", err, out, desc)
	}

	// Port-forward to access the gateway.
	// Use kubectl exec + curl instead to avoid port-forward complexity.
	// Test health endpoint via exec.
	healthCmd := kubectl("exec", "inference-gw-test", "-n", ns, "--",
		"wget", "-q", "-O-", "http://localhost:8402/health")
	healthOut, err := healthCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("health check failed: %v\n%s", err, healthOut)
	}
	if !strings.Contains(string(healthOut), "ok") {
		t.Errorf("health response doesn't contain 'ok': %s", healthOut)
	}

	// Test attestation endpoint.
	attestCmd := kubectl("exec", "inference-gw-test", "-n", ns, "--",
		"wget", "-q", "-O-", "http://localhost:8402/v1/attestation")
	attestOut, err := attestCmd.CombinedOutput()
	if err != nil {
		t.Logf("attestation output: %s", attestOut)
		t.Fatalf("attestation endpoint failed: %v", err)
	}

	// Parse attestation response.
	var report struct {
		TEEType   string `json:"tee_type"`
		Pubkey    string `json:"pubkey"`
		ModelHash string `json:"model_hash"`
	}
	if err := json.Unmarshal(attestOut, &report); err != nil {
		t.Fatalf("parse attestation: %v\nraw: %s", err, attestOut)
	}

	if report.TEEType != "stub" {
		t.Errorf("tee_type = %q, want %q", report.TEEType, "stub")
	}
	if report.Pubkey == "" {
		t.Error("pubkey should not be empty")
	}
	if report.ModelHash == "" {
		t.Error("model_hash should not be empty")
	}

	t.Logf("Attestation from CoCo pod: tee_type=%s pubkey=%s...%s",
		report.TEEType, report.Pubkey[:16], report.Pubkey[len(report.Pubkey)-8:])
	t.Log("Inference gateway successfully running inside CoCo dev VM")
}
