package stack

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// The k3d node (a Docker container) gets a memory cap and no swap, so the
// stack's pods can't take the whole host down with them: when they need
// more than the cap, the kernel OOM-kills inside the node instead of picking
// Tailscale, the dev containers or the desktop. k3d's serversMemory sets the
// container's memory limit and fakes the node's /proc/meminfo, so Kubernetes
// sees the cap as the node's capacity; it leaves swap on, which Up then turns
// off with docker update.

const (
	gib = int64(1) << 30
	mib = int64(1) << 20

	// Hosts with less than this much memory for Docker get no cap: the
	// headroom would leave too little for a node to be useful.
	minCappedTotal = 12 * gib
)

// NodeMemoryEnv overrides the node memory cap: a size (22g, 22Gi, 22528m),
// "auto" (the default) or "off". It applies when the cluster is created (and
// to a cluster created before obol capped it); a cap already on the node
// changes only with docker update.
const NodeMemoryEnv = "OBOL_NODE_MEMORY"

// Swappable for tests.
var (
	dockerMemTotalFn = func() (int64, error) {
		out, err := exec.Command("docker", "info", "--format", "{{.MemTotal}}").Output()
		if err != nil {
			return 0, err
		}

		return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	}
	dockerOutputFn = func(args ...string) (string, error) {
		out, err := exec.Command("docker", args...).Output()
		return strings.TrimSpace(string(out)), err
	}
)

// defaultNodeMemory leaves headroom for the host: a quarter of Docker's
// memory, at least 4 GiB and at most 8 GiB. 31 GiB → 23.25 GiB, 16 GiB →
// 12 GiB, 64 GiB → 56 GiB. 0 means no cap.
func defaultNodeMemory(total int64) int64 {
	if total < minCappedTotal {
		return 0
	}

	headroom := min(max(total/4, 4*gib), 8*gib)

	return total - headroom
}

var sizePattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([kmgt]?)i?b?$`)

// parseNodeMemory reads a size the way Docker does (binary units, any case,
// optional "i"/"b": 22g = 22Gi = 22GiB). "" and "auto" return auto=true;
// "off", "none" and "0" return 0.
func parseNodeMemory(s string) (bytes int64, auto bool, err error) {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "", "auto":
		return 0, true, nil
	case "off", "none", "0":
		return 0, false, nil
	}

	m := sizePattern.FindStringSubmatch(v)
	if m == nil {
		return 0, false, fmt.Errorf("invalid node memory %q: want a size such as 22g or 22Gi, auto or off", s)
	}

	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid node memory %q: %w", s, err)
	}

	unit := map[string]float64{"": 1, "k": 1 << 10, "m": 1 << 20, "g": 1 << 30, "t": 1 << 40}[m[2]]
	b := int64(n * unit)

	if b < 2*gib {
		return 0, false, fmt.Errorf("node memory %q is below 2 GiB, too small to run the stack", s)
	}

	return b, false, nil
}

// resolveNodeMemory turns the user's setting into a cap in bytes (0 = none).
func resolveNodeMemory(setting string) (int64, error) {
	b, auto, err := parseNodeMemory(setting)
	if err != nil || !auto {
		return b, err
	}

	total, err := dockerMemTotalFn()
	if err != nil {
		return 0, nil //nolint:nilerr // can't size it, so don't cap it
	}

	return defaultNodeMemory(total), nil
}

// k3dSize renders bytes as a k3d/Docker size in whole MiB.
func k3dSize(b int64) string {
	if b <= 0 {
		return ""
	}

	return fmt.Sprintf("%dm", b/mib)
}

func humanGiB(b int64) string {
	return strconv.FormatFloat(float64(b)/float64(gib), 'f', 1, 64) + " GiB"
}

var serversMemoryLine = regexp.MustCompile(`(?m)^\s*serversMemory:\s*"?([^"\n]*)"?\s*$`)

// configuredNodeMemory reads serversMemory from a rendered k3d.yaml.
// present is false for configs written before obol set a cap.
func configuredNodeMemory(k3dConfig string) (bytes int64, present bool) {
	m := serversMemoryLine.FindStringSubmatch(k3dConfig)
	if m == nil {
		return 0, false
	}

	b, _, err := parseNodeMemory(m[1])
	if err != nil || m[1] == "" {
		return 0, true
	}

	return b, true
}

// enforceNodeMemory caps the cluster's server containers and turns their
// swap off. It never lowers or raises a cap that is already set (one set by
// hand with docker update is kept); it only adds a missing cap and removes
// swap. Clusters created before obol capped them get the default cap here,
// but Kubernetes keeps seeing the host's full memory until the cluster is
// recreated.
func enforceNodeMemory(u *ui.UI, clusterName, k3dConfig string) {
	want, present := configuredNodeMemory(k3dConfig)
	if !present {
		var err error
		if want, err = resolveNodeMemory(os.Getenv(NodeMemoryEnv)); err != nil {
			u.Warnf("Not capping the cluster's memory: %v", err)
			return
		}
	}

	names, err := dockerOutputFn("ps", "-a", "--filter", "label=k3d.cluster="+clusterName,
		"--filter", "label=k3d.role=server", "--format", "{{.Names}}")
	if err != nil {
		u.Warnf("Could not check the cluster's memory cap: %v", err)
		return
	}

	for _, name := range strings.Fields(names) {
		out, err := dockerOutputFn("inspect", "-f", "{{.HostConfig.Memory}} {{.HostConfig.MemorySwap}}", name)
		if err != nil {
			u.Warnf("Could not check %s's memory cap: %v", name, err)
			continue
		}

		var memory, swap int64
		if _, err := fmt.Sscan(out, &memory, &swap); err != nil {
			continue
		}

		target := memory
		if target == 0 {
			target = want
		}

		if target == 0 || swap == target {
			continue
		}

		if _, err := dockerOutputFn("update", "--memory", strconv.FormatInt(target, 10),
			"--memory-swap", strconv.FormatInt(target, 10), name); err != nil {
			u.Warnf("Could not cap %s at %s without swap: %v", name, humanGiB(target), err)
			continue
		}

		if memory == 0 {
			u.Infof("Capped the cluster node at %s of memory, no swap. To change it: docker update --memory <size> --memory-swap <size> %s",
				humanGiB(target), name)
			if !present {
				u.Dim("  Kubernetes keeps scheduling against the host's full memory until the cluster is next created.")
			}
		} else {
			u.Infof("Turned swap off for the cluster node (memory cap %s)", humanGiB(target))
		}
	}
}
