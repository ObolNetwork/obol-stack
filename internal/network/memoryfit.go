package network

import (
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// clusterMemoryFn returns the nodes' allocatable memory and the memory
// requested by every live pod outside skipNamespace, in MiB. ok is false
// when the cluster can't be asked. Swappable for tests.
var clusterMemoryFn = func(cfg *config.Config, skipNamespace string) (allocatableMi, requestedMi int64, ok bool) {
	nodesOut, err := stopKubectlFn(cfg, "get", "nodes", "--request-timeout=5s", "-o", "json")
	if err != nil {
		return 0, 0, false
	}

	var nodes corev1.NodeList
	if json.Unmarshal([]byte(nodesOut), &nodes) != nil {
		return 0, 0, false
	}

	for _, n := range nodes.Items {
		if q, found := n.Status.Allocatable[corev1.ResourceMemory]; found {
			allocatableMi += q.Value() >> 20
		}
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

	return allocatableMi, requestedMi, allocatableMi > 0
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

// warnMemoryFit warns when the deployment's clients would not fit next to
// everything else in the cluster: the scheduler then leaves pods Pending.
// It compares against the nodes' allocatable memory, which is the k3d cap
// only for clusters created with one; a cap added later with docker update
// isn't visible to Kubernetes, so there the warning can come too late.
func warnMemoryFit(cfg *config.Config, u *ui.UI, d Deployment) {
	if d.Stopped {
		return
	}

	el, cl, err := deploymentSizing(d)
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

	u.Warnf("Memory requests would total %s, more than the cluster node's %s: some pods will stay Pending.",
		formatMi(total), formatMi(allocatable))
	u.Print(strings.Join([]string{
		"  Stop a node you don't need:   obol network stop <network>/<id>",
		"  Lower a client's limit:       executionMemory / consensusMemory in values.yaml (the request never exceeds it)",
		"  Give the node more memory:    docker update --memory <size> --memory-swap <size> k3d-<cluster>-server-0",
		"                                (Kubernetes keeps the old size until the cluster is next created)",
	}, "\n"))
}
