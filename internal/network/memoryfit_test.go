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
	orig := clusterMemoryFn
	t.Cleanup(func() { clusterMemoryFn = orig })

	// mainnet archive reth + silver requests 6Gi + 4Gi = 10Gi.
	d := Deployment{Namespace: "ethereum-mainnet", Network: "mainnet", Mode: modeArchive, ExecutionClient: "reth", ConsensusClient: "silver"}

	cases := []struct {
		name                string
		allocatable, others int64
		ok, stopped         bool
		warn                bool
	}{
		{"fits", gi(23), gi(10), true, false, false},
		{"exactly fits", gi(20), gi(10), true, false, false},
		{"over", gi(23), gi(14), true, false, true},
		{"stopped node needs nothing", gi(23), gi(14), true, true, false},
		{"cluster unreachable", 0, 0, false, false, false},
	}
	for _, c := range cases {
		clusterMemoryFn = func(*config.Config, string) (int64, int64, bool) { return c.allocatable, c.others, c.ok }

		dd := d
		dd.Stopped = c.stopped

		var out bytes.Buffer
		warnMemoryFit(&config.Config{}, ui.NewForTest(&out, &out), dd)

		if got := strings.Contains(out.String(), "stay Pending"); got != c.warn {
			t.Errorf("%s: warned = %v, want %v (%q)", c.name, got, c.warn, out.String())
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
