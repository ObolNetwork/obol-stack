package network

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/validate"
)

// Swappable for tests (stop/start talk to the cluster only through these).
var (
	stopEnsureClusterFn = kubectl.EnsureCluster
	stopKubectlFn       = func(cfg *config.Config, args ...string) (string, error) {
		bin, kubeconfig := kubectl.Paths(cfg)
		return kubectl.Output(bin, kubeconfig, args...)
	}
	stopRegisterFn   = RegisterERPCUpstream
	stopDeregisterFn = DeregisterERPCUpstream
	stopSyncFn       = Sync
	stopDiskUsageFn  = func(cfg *config.Config, namespace string) uint64 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		n, _, _ := dirUsage(ctx, filepath.Join(cfg.DataDir, namespace))

		return n
	}
)

// stoppedLine matches the `stopped:` key written into values.yaml.
var stoppedLine = regexp.MustCompile(`(?m)^stopped:.*$`)

// stoppableTypes are the network types whose chart renders `stopped` as
// replicas 0 (see internal/embed/networks/ethereum/helmfile.yaml.gotmpl).
var stoppableTypes = map[string]bool{TypeEthereum: true}

// Stop scales a deployment's node workloads to 0, keeping its PVCs, takes
// its local upstream out of eRPC (traffic falls back to remote RPCs) and
// records `stopped: true` in its values.yaml so `network sync` and
// `stack up` keep it down. With the cluster down only the record is
// written; it applies on the next `obol stack up`.
func Stop(cfg *config.Config, u *ui.UI, identifier string) error {
	networkType, id, valuesPath, err := stoppableDeployment(cfg, identifier)
	if err != nil {
		return err
	}

	ident := networkType + "/" + id
	namespace := networkType + "-" + id

	changed, err := setValuesStopped(valuesPath, true)
	if err != nil {
		return err
	}

	if !changed {
		u.Infof("%s is already stopped; making sure it is scaled down", ident)
	}

	if err := stopEnsureClusterFn(cfg); err != nil {
		u.Warnf("Cluster not reachable (%v)", err)
		u.Successf("Recorded %s as stopped; it stays down on the next obol stack up", ident)
		u.Printf("  Start again: obol network start %s", ident)

		return nil
	}

	scaled, err := scaleWorkloads(cfg, namespace, 0)
	if err != nil {
		return fmt.Errorf("scale %s to 0: %w (recorded as stopped; retry obol network stop %s)", namespace, err, ident)
	}

	if len(scaled) == 0 {
		u.Successf("Nothing running in %s", namespace)
	} else {
		u.Successf("Scaled to 0: %s", strings.Join(scaled, ", "))
	}

	if err := stopDeregisterFn(cfg, networkType, id); err != nil {
		u.Warnf("Could not remove %s from eRPC: %v", LocalUpstreamID(networkType, id), err)
	} else {
		u.Successf("Removed %s from eRPC; requests fall back to remote RPCs", LocalUpstreamID(networkType, id))
	}

	data := "Data kept"
	if n := stopDiskUsageFn(cfg, namespace); n > 0 {
		data = fmt.Sprintf("Data kept (%s)", FormatBytes(n))
	}

	u.Successf("%s stopped. %s; network sync and stack up keep it stopped", ident, data)
	u.Printf("  Start again: obol network start %s", ident)

	return nil
}

// Start undoes Stop: clears `stopped`, scales the workloads back to 1 (or
// runs a full sync when the cluster no longer has them, e.g. after it was
// recreated) and registers the node with eRPC again.
func Start(cfg *config.Config, u *ui.UI, identifier string) error {
	networkType, id, valuesPath, err := stoppableDeployment(cfg, identifier)
	if err != nil {
		return err
	}

	ident := networkType + "/" + id
	namespace := networkType + "-" + id

	changed, err := setValuesStopped(valuesPath, false)
	if err != nil {
		return err
	}

	if !changed {
		u.Infof("%s was not stopped; making sure it is running", ident)
	}

	if err := stopEnsureClusterFn(cfg); err != nil {
		return fmt.Errorf("cluster not reachable: %w\n%s is no longer marked stopped; obol stack up will start it", err, ident)
	}

	names, err := stopKubectlFn(cfg, "get", "statefulset,deployment", "-n", namespace, "-o", "name")
	if err != nil || strings.TrimSpace(names) == "" {
		u.Infof("No workloads in %s; deploying %s", namespace, ident)
		return stopSyncFn(cfg, u, ident)
	}

	scaled, err := scaleWorkloads(cfg, namespace, 1)
	if err != nil {
		return fmt.Errorf("scale %s to 1: %w", namespace, err)
	}

	u.Successf("Scaled to 1: %s", strings.Join(scaled, ", "))

	if err := stopRegisterFn(cfg, networkType, id); err != nil {
		u.Warnf("Could not register %s with eRPC (run obol network sync %s): %v", LocalUpstreamID(networkType, id), ident, err)
	} else {
		u.Successf("Registered %s with eRPC (used once synced; remote RPCs serve until then)", LocalUpstreamID(networkType, id))
	}

	u.Successf("%s started; it resumes syncing where it stopped", ident)
	u.Printf("  Follow it: obol network status %s --watch", ident)

	return nil
}

// stoppableDeployment resolves "<type>/<id>" to an installed deployment of
// a type that supports stop/start.
func stoppableDeployment(cfg *config.Config, identifier string) (networkType, id, valuesPath string, err error) {
	networkType, id, ok := strings.Cut(identifier, "/")
	if !ok || networkType == "" || id == "" {
		return "", "", "", fmt.Errorf("invalid deployment %q, want <network>/<id>", identifier)
	}

	if !stoppableTypes[networkType] {
		return "", "", "", fmt.Errorf("stop/start is supported for ethereum deployments only (got %s)", identifier)
	}

	// The id becomes a path component (and the <type>-<id> namespace): it
	// must be a DNS label and name a deployment that is actually installed.
	if err := validate.Name(id); err != nil {
		return "", "", "", fmt.Errorf("invalid deployment %q: %w", identifier, err)
	}

	if !slices.Contains(ListDeployments(cfg), networkType+"/"+id) {
		return "", "", "", fmt.Errorf("deployment %s not found", identifier)
	}

	networksDir := filepath.Join(cfg.ConfigDir, "networks")
	valuesPath = filepath.Join(networksDir, networkType, id, "values.yaml")

	if rel, err := filepath.Rel(networksDir, valuesPath); err != nil || !filepath.IsLocal(rel) {
		return "", "", "", fmt.Errorf("invalid deployment %q", identifier)
	}

	if _, err := os.Stat(valuesPath); err != nil {
		return "", "", "", fmt.Errorf("deployment %s not found (%s)", identifier, valuesPath)
	}

	return networkType, id, valuesPath, nil
}

// scaleWorkloads scales every StatefulSet and Deployment in namespace and
// returns them as kind/name. No workloads is not an error.
func scaleWorkloads(cfg *config.Config, namespace string, replicas int) ([]string, error) {
	out, err := stopKubectlFn(cfg, "scale", "statefulset,deployment", "--all",
		"-n", namespace, fmt.Sprintf("--replicas=%d", replicas))
	if err != nil {
		if strings.Contains(err.Error(), "no objects passed to scale") || strings.Contains(out, "No resources found") {
			return nil, nil
		}

		return nil, err
	}

	var scaled []string

	for line := range strings.SplitSeq(out, "\n") {
		// "statefulset.apps/ethereum-beacon scaled"
		name, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		if name == "" || !strings.Contains(name, "/") {
			continue
		}

		kind, rest, _ := strings.Cut(name, "/")
		kind, _, _ = strings.Cut(kind, ".")
		scaled = append(scaled, kind+"/"+rest)
	}

	return scaled, nil
}

// setValuesStopped writes `stopped: <v>` into a deployment's values.yaml,
// keeping the rest of the file (comments included). It reports whether the
// value changed.
func setValuesStopped(valuesPath string, stopped bool) (bool, error) {
	raw, err := os.ReadFile(valuesPath)
	if err != nil {
		return false, err
	}

	content := string(raw)
	line := fmt.Sprintf("stopped: %t", stopped)

	switch existing := stoppedLine.FindString(content); {
	case existing != "":
		if strings.TrimSpace(existing) == line {
			return false, nil
		}

		content = stoppedLine.ReplaceAllString(content, line)
	case !stopped:
		return false, nil
	default:
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}

		content += "\n# Set by obol network stop; obol network start clears it.\n# While true the node's workloads render with 0 replicas.\n" + line + "\n"
	}

	info, err := os.Stat(valuesPath)
	if err != nil {
		return false, err
	}

	if err := os.WriteFile(valuesPath, []byte(content), info.Mode().Perm()); err != nil { //nolint:gosec // G703: stoppableDeployment validated the id and checked the path is an installed deployment under $CONFIG/networks
		return false, fmt.Errorf("write %s: %w", valuesPath, err)
	}

	return true, nil
}

// deploymentStopped reports whether "<type>/<id>" is recorded as stopped.
func deploymentStopped(cfg *config.Config, networkType, id string) bool {
	return readDeployment(cfg, networkType, id).Stopped
}

// FormatBytes renders bytes as "412 GiB" / "1.1 TiB".
func FormatBytes(n uint64) string {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
		tib = 1 << 40
	)

	switch {
	case n >= tib:
		return fmt.Sprintf("%.1f TiB", float64(n)/tib)
	case n >= 10*gib:
		return fmt.Sprintf("%.0f GiB", float64(n)/gib)
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	case n >= mib:
		return fmt.Sprintf("%.0f MiB", float64(n)/mib)
	case n >= kib:
		return fmt.Sprintf("%.0f KiB", float64(n)/kib)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
