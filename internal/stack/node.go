package stack

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// k3sAPIPort is the standard k3s supervisor/apiserver port a joining agent dials.
const k3sAPIPort = 6443

// OutboundIP returns this host's primary outbound IPv4 address, discovered by
// opening a UDP socket toward a public address (no packets are actually sent).
// It is the address a LAN peer would use to reach this host and the one k3s
// advertises as the node InternalIP. Falls back to 127.0.0.1.
func OutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()

	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
		return addr.IP.String()
	}

	return "127.0.0.1"
}

// nodeHostname returns this host's hostname, or "localhost" if unavailable.
func nodeHostname() string {
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		return "localhost"
	}

	return h
}

// K3sNodeTokenPath returns the path to the k3s server join token for the k3s
// backend's data-dir. It mirrors `data-dir: {{DATA_DIR}}/k3s` in the embedded
// k3s-config.yaml — NOT the default /var/lib/rancher/k3s, which obol overrides.
func K3sNodeTokenPath(cfg *config.Config) string {
	absDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		absDataDir = cfg.DataDir
	}

	return filepath.Join(absDataDir, "k3s", "server", "node-token")
}

// ReadK3sNodeToken reads the root-owned k3s server join token via sudo.
func ReadK3sNodeToken(cfg *config.Config) (string, error) {
	path := K3sNodeTokenPath(cfg)

	out, err := exec.Command("sudo", "cat", path).Output()
	if err != nil {
		return "", fmt.Errorf("read k3s node-token at %s (is this host the running k3s server?): %w", path, err)
	}

	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("k3s node-token at %s is empty", path)
	}

	return token, nil
}

// K3sServerURL returns the https URL a joining agent dials. When override is
// empty it uses this host's primary LAN IP and the standard k3s API port.
func K3sServerURL(override string) string {
	if override != "" {
		return override
	}

	return fmt.Sprintf("https://%s:%d", OutboundIP(), k3sAPIPort)
}

// K3sBinaryVersion returns the k3s release string (e.g. "v1.35.5+k3s1") of the
// k3s binary in BinDir, used to pin a joining agent to the server's version.
// Returns "" when it can't be determined (the installer then picks stable).
func K3sBinaryVersion(cfg *config.Config) string {
	out, err := exec.Command(filepath.Join(cfg.BinDir, "k3s"), "--version").Output()
	if err != nil {
		return ""
	}

	return parseK3sVersion(string(out))
}

// parseK3sVersion extracts the version token from `k3s --version` output,
// whose first line looks like: "k3s version v1.35.5+k3s1 (6a4781ad)".
func parseK3sVersion(out string) string {
	firstLine, _, _ := strings.Cut(out, "\n")

	fields := strings.Fields(firstLine)
	for i, f := range fields {
		if f == "version" && i+1 < len(fields) {
			return fields[i+1]
		}
	}

	return ""
}

// K3sAgentJoinCommand builds the copy-pasteable one-liner an operator runs on a
// Linux worker node to join this stack's k3s cluster. When version is non-empty
// the agent install is pinned to it (agents should match the server version).
func K3sAgentJoinCommand(serverURL, token, version string) string {
	var b strings.Builder

	b.WriteString("curl -sfL https://get.k3s.io | ")

	if version != "" {
		b.WriteString("INSTALL_K3S_VERSION=" + version + " ")
	}

	b.WriteString("K3S_URL=" + serverURL + " ")
	b.WriteString("K3S_TOKEN='" + token + "' ")
	b.WriteString("sh -s - agent")

	return b.String()
}
