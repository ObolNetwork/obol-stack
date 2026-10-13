package embed

import (
	"strings"
	"testing"
)

// TestEthereumHelmfile_ServiceMonitorsSelectedByMonitoring guards EL/CL
// metrics: kube-prometheus-stack only selects monitors labelled
// release=monitoring (values/monitoring.yaml.gotmpl), so a ServiceMonitor
// without that label is silently never scraped.
func TestEthereumHelmfile_ServiceMonitorsSelectedByMonitoring(t *testing.T) {
	data, err := ReadEmbeddedNetworkFile("ethereum", "helmfile.yaml.gotmpl")
	if err != nil {
		t.Fatal(err)
	}

	monitoring, err := ReadInfrastructureFile("values/monitoring.yaml.gotmpl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(monitoring), "release: monitoring") {
		t.Fatal("monitoring selector no longer keys on release=monitoring; update the network ServiceMonitor labels")
	}

	block := "serviceMonitor:\n            enabled: true\n            labels:\n              release: monitoring"
	if got := strings.Count(string(data), block); got != 2 {
		t.Fatalf("found %d EL/CL serviceMonitor blocks labelled release=monitoring, want 2 (execution + consensus)", got)
	}
}
