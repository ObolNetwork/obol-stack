package embed

import (
	"regexp"
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

	// The consensus block is disabled for Silver, which serves /metrics on
	// its own port and is scraped by its own ServiceMonitor instead (below).
	block := regexp.MustCompile(`serviceMonitor:\n            enabled: (true|\{\{ ne \.Values\.consensusClient "silver" \}\})\n            labels:\n              release: monitoring`)
	if got := len(block.FindAllString(string(data), -1)); got != 2 {
		t.Fatalf("found %d EL/CL serviceMonitor blocks labelled release=monitoring, want 2 (execution + consensus)", got)
	}

	if !strings.Contains(string(data), "kind: ServiceMonitor\n            metadata:\n              name: ethereum-beacon-silver\n              namespace: ethereum-{{ .Values.id }}\n              labels:\n                release: monitoring") {
		t.Fatal("Silver metrics ServiceMonitor missing or not labelled release=monitoring")
	}
}
