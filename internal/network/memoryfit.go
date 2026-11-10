package network

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// nodeAllocatableFn returns the memory the cluster's nodes can give pods,
// in MiB: their allocatable memory, or the k3d node's Docker cap when that
// is lower (a cap added to a cluster created before obol set one isn't
// visible to Kubernetes). ok is false when the cluster can't be asked.
// Swappable for tests.
var nodeAllocatableFn = func(cfg *config.Config) (int64, bool) {
	allocatableMi, ok := kubeAllocatableFn(cfg)
	if !ok {
		return 0, false
	}

	if capMi, ok := dockerNodeCapMi(cfg); ok && capMi < allocatableMi {
		allocatableMi = capMi
	}

	return allocatableMi, true
}

// kubeAllocatableFn is the nodes' allocatable memory as Kubernetes sees it
// (and schedules against), in MiB. Swappable for tests.
var kubeAllocatableFn = func(cfg *config.Config) (int64, bool) {
	nodesOut, err := stopKubectlFn(cfg, "get", "nodes", "--request-timeout=5s", "-o", "json")
	if err != nil {
		return 0, false
	}

	var nodes corev1.NodeList
	if json.Unmarshal([]byte(nodesOut), &nodes) != nil {
		return 0, false
	}

	var allocatableMi int64
	for _, n := range nodes.Items {
		if q, found := n.Status.Allocatable[corev1.ResourceMemory]; found {
			allocatableMi += q.Value() >> 20
		}
	}

	return allocatableMi, allocatableMi > 0
}

// dockerNodeCapMi is the memory cap on the stack's k3d server containers,
// summed, in MiB. ok is false without Docker (the k3s backend), without a
// k3d cluster, or when a server has no cap.
func dockerNodeCapMi(cfg *config.Config) (int64, bool) {
	id, err := os.ReadFile(filepath.Join(cfg.ConfigDir, ".stack-id"))
	if err != nil || strings.TrimSpace(string(id)) == "" {
		return 0, false
	}

	names, err := exec.Command("docker", "ps", "--filter", "label=k3d.cluster=obol-stack-"+strings.TrimSpace(string(id)),
		"--filter", "label=k3d.role=server", "--format", "{{.Names}}").Output()
	if err != nil {
		return 0, false
	}

	var totalMi int64
	for name := range strings.FieldsSeq(string(names)) {
		out, err := exec.Command("docker", "inspect", "-f", "{{.HostConfig.Memory}}", name).Output()
		if err != nil {
			return 0, false
		}

		b, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil || b <= 0 {
			return 0, false
		}

		totalMi += b >> 20
	}

	return totalMi, totalMi > 0
}

// clusterMemoryFn returns the nodes' allocatable memory (see
// nodeAllocatableFn) and the memory requested by every live pod outside
// skipNamespace, in MiB. ok is false when the cluster can't be asked.
// Swappable for tests.
var clusterMemoryFn = func(cfg *config.Config, skipNamespace string) (allocatableMi, requestedMi int64, ok bool) {
	allocatableMi, ok = nodeAllocatableFn(cfg)
	if !ok {
		return 0, 0, false
	}

	podsOut, err := stopKubectlFn(cfg, "get", "pods", "-A", "--request-timeout=5s", "-o", "json")
	if err != nil {
		return 0, 0, false
	}

	var pods corev1.PodList
	if json.Unmarshal([]byte(podsOut), &pods) != nil {
		return 0, 0, false
	}

	for _, p := range pods.Items {
		if p.Namespace == skipNamespace || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}

		q := podMemoryRequest(p.Spec)
		requestedMi += q.Value() >> 20
	}

	return allocatableMi, requestedMi, true
}

// podMemoryRequest is what the scheduler reserves for a pod: the sum of its
// containers' requests, or its largest init container's if that is more.
func podMemoryRequest(spec corev1.PodSpec) resource.Quantity {
	var sum resource.Quantity
	for _, c := range spec.Containers {
		sum.Add(c.Resources.Requests[corev1.ResourceMemory])
	}

	for _, c := range spec.InitContainers {
		if q := c.Resources.Requests[corev1.ResourceMemory]; q.Cmp(sum) > 0 {
			sum = q
		}
	}

	return sum
}

// clampToNode keeps a client's limit (and so the request and the heap sizes
// derived from it) within the node: a limit the node can't supply would get
// the client killed by the node's OOM instead of its own, and size its heap
// for memory that isn't there.
func clampToNode(s MemorySizing, nodeMi int64) MemorySizing {
	if nodeMi <= 0 || s.LimitMi <= nodeMi {
		return s
	}

	s.LimitMi = nodeMi
	s.RequestMi = min(s.RequestMi, nodeMi)

	return s
}

// clusterSizing is the sizing a sync applies: the table and the user's
// overrides, with limits clamped to the node when the cluster can be asked.
// nodeMi is the node's memory when it clamped a limit, else 0.
func clusterSizing(cfg *config.Config, d Deployment) (el, cl MemorySizing, nodeMi int64, err error) {
	el, cl, err = deploymentSizing(d)
	if err != nil {
		return el, cl, 0, err
	}

	allocatable, ok := nodeAllocatableFn(cfg)
	if !ok {
		return el, cl, 0, nil
	}

	if el.LimitMi > allocatable || cl.LimitMi > allocatable {
		nodeMi = allocatable
	}

	return clampToNode(el, allocatable), clampToNode(cl, allocatable), nodeMi, nil
}

// warnMemoryFit warns when the deployment's clients would not fit next to
// everything else in the cluster: the scheduler then leaves pods Pending.
func warnMemoryFit(cfg *config.Config, u *ui.UI, d Deployment) {
	if d.Stopped {
		return
	}

	el, cl, _, err := clusterSizing(cfg, d)
	if err != nil {
		return
	}

	allocatable, others, ok := clusterMemoryFn(cfg, d.Namespace)
	if !ok {
		return
	}

	total := others + el.RequestMi + cl.RequestMi
	if total <= allocatable {
		return
	}

	// A Docker cap added after the cluster was created is invisible to the
	// scheduler: the pods still start, but can run the node out of memory
	// together instead of waiting Pending.
	if kubeMi, ok := kubeAllocatableFn(cfg); ok && kubeMi > allocatable {
		u.Warnf("Memory requests would total %s, more than the cluster node's %s memory cap. Kubernetes sees %s "+
			"(the cap was added after the cluster was created), so the pods still start, but together they can run the node "+
			"out of memory and get OOM-killed.", formatMi(total), formatMi(allocatable), formatMi(kubeMi))
	} else {
		u.Warnf("Memory requests would total %s, more than the cluster node's %s: some pods will stay Pending.",
			formatMi(total), formatMi(allocatable))
	}

	u.Dim("  Upgrading obol? Your other nodes' requests may drop when you sync them too.")
	u.Print(strings.Join([]string{
		"  Stop a node you don't need:  obol network stop <network>/<id>",
		fmt.Sprintf("  Or lower this node's memory: executionMemory / consensusMemory in values.yaml (requests now %s / %s;",
			formatMi(el.RequestMi), formatMi(cl.RequestMi)),
		"                               below them, a client is likely to be OOM-killed)",
	}, "\n"))
}
