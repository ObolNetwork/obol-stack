package network

import (
	"bytes"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestWarnMemoryFit(t *testing.T) {
	orig, origNode, origKube := clusterMemoryFn, nodeAllocatableFn, kubeAllocatableFn
	t.Cleanup(func() { clusterMemoryFn, nodeAllocatableFn, kubeAllocatableFn = orig, origNode, origKube })

	// mainnet archive reth + silver requests 6Gi + 4Gi = 10Gi.
	d := Deployment{Namespace: "ethereum-mainnet", Network: "mainnet", Mode: modeArchive, ExecutionClient: "reth", ConsensusClient: "silver"}

	cases := []struct {
		name                      string
		allocatable, kube, others int64
		ok, stopped               bool
		want                      string // "" = no warning
	}{
		{"fits", gi(23), gi(23), gi(10), true, false, ""},
		{"exactly fits", gi(20), gi(20), gi(10), true, false, ""},
		{"over", gi(23), gi(23), gi(14), true, false, "stay Pending"},
		{"over a cap Kubernetes can't see", gi(22), gi(31), gi(14), true, false, "Kubernetes sees 31Gi"},
		{"stopped node needs nothing", gi(23), gi(23), gi(14), true, true, ""},
		{"cluster unreachable", 0, 0, 0, false, false, ""},
	}
	for _, c := range cases {
		clusterMemoryFn = func(*config.Config, string) (int64, int64, bool) { return c.allocatable, c.others, c.ok }
		nodeAllocatableFn = func(*config.Config) (int64, bool) { return c.allocatable, c.ok }
		kubeAllocatableFn = func(*config.Config) (int64, bool) { return c.kube, c.ok }

		dd := d
		dd.Stopped = c.stopped

		var out bytes.Buffer
		warnMemoryFit(&config.Config{}, ui.NewForTest(&out, &out), dd)

		if c.want == "" && out.Len() > 0 {
			t.Errorf("%s: unexpected warning %q", c.name, out.String())
		}

		if c.want != "" && !strings.Contains(out.String(), c.want) {
			t.Errorf("%s: warning %q, want it to contain %q", c.name, out.String(), c.want)
		}
	}
}

func TestPodMemoryRequest(t *testing.T) {
	req := func(s string) corev1.ResourceRequirements {
		return corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(s)}}
	}

	spec := corev1.PodSpec{
		Containers:     []corev1.Container{{Resources: req("1Gi")}, {Resources: req("512Mi")}, {}},
		InitContainers: []corev1.Container{{Resources: req("256Mi")}},
	}
	if got := podMemoryRequest(spec); got.Value() != 1536<<20 {
		t.Errorf("containers sum: got %s, want 1536Mi", got.String())
	}

	spec.InitContainers = []corev1.Container{{Resources: req("2Gi")}}
	if got := podMemoryRequest(spec); got.Value() != 2<<30 {
		t.Errorf("larger init container: got %s, want 2Gi", got.String())
	}
}
