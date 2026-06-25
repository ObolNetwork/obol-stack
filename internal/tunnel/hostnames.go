package tunnel

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// AddHostnameOptions configures `obol tunnel hostname add`.
type AddHostnameOptions struct {
	Hostname string

	// OverwriteDNS forwards --overwrite-dns to `cloudflared tunnel route dns`
	// (local-managed only) so an existing A/AAAA/CNAME at the hostname is
	// replaced instead of failing with Cloudflare API error 1003.
	OverwriteDNS bool
}

// RemoveHostnameOptions configures `obol tunnel hostname remove`.
type RemoveHostnameOptions struct {
	Hostname string
}

// HostnameInfo is one entry in the hostname listing.
type HostnameInfo struct {
	Hostname string `json:"hostname"`
	Primary  bool   `json:"primary"`
	URL      string `json:"url"`
}

// HostnameListResult is the JSON-serialisable result of ListHostnames.
type HostnameListResult struct {
	ManagementMode string         `json:"management_mode"`
	Hostnames      []HostnameInfo `json:"hostnames"`
}

// HostnameMutationResult is the JSON-serialisable result of Add/RemoveHostname.
type HostnameMutationResult struct {
	Hostname       string         `json:"hostname"`
	Action         string         `json:"action"` // "added" | "removed"
	ManagementMode string         `json:"management_mode"`
	Hostnames      []HostnameInfo `json:"hostnames"`
}

// ListHostnames returns every public hostname tracked for the tunnel, primary
// first. Returns an empty list for quick/dormant tunnels.
func ListHostnames(cfg *config.Config) (*HostnameListResult, error) {
	st, err := loadTunnelState(cfg)
	if err != nil {
		return nil, fmt.Errorf("load tunnel state: %w", err)
	}
	if st == nil || !st.IsPersistent() {
		return &HostnameListResult{ManagementMode: tunnelManagementQuick}, nil
	}
	return &HostnameListResult{
		ManagementMode: st.Management(),
		Hostnames:      hostnameInfos(st.HostnameSet()),
	}, nil
}

// AddHostname adds a second (or Nth) public hostname to an existing persistent
// tunnel WITHOUT disturbing the hostnames already live. It regenerates the
// cloudflared ingress over the full set (local-managed) and updates the
// storefront HTTPRoute to list every hostname.
//
//   - Local-managed (browser cert): routes a DNS CNAME for the new hostname via
//     cloudflared (cert-scoped, no API token), re-renders the connector ingress
//     over all hostnames, and reloads the connector.
//   - Dashboard-managed (connector token): obol holds no account-wide API token
//     by design, so it cannot edit the dashboard tunnel's ingress. It tracks the
//     hostname, updates the in-cluster storefront route, and prints the exact
//     dashboard step the operator must add.
func AddHostname(cfg *config.Config, u *ui.UI, opts AddHostnameOptions) (*HostnameMutationResult, error) {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		return nil, errors.New("hostname is required (e.g. data.example.com)")
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, errors.New("stack not running, use 'obol stack up' first")
	}

	st, err := loadTunnelState(cfg)
	if err != nil {
		return nil, fmt.Errorf("load tunnel state: %w", err)
	}
	if st == nil || !st.IsPersistent() {
		return nil, errors.New("no permanent tunnel configured; run 'obol tunnel setup' first, then add more hostnames")
	}

	existing := st.HostnameSet()
	for _, h := range existing {
		if h == hostname {
			return nil, fmt.Errorf("%s is already a tunnel hostname; nothing to do", hostname)
		}
	}

	// Full set = current set + new hostname (appended, primary unchanged).
	updated := normalizeHostnames(append(append([]string{}, existing...), hostname))

	switch st.Management() {
	case tunnelManagementLocal:
		if err := addHostnameLocal(cfg, u, kubeconfigPath, st, hostname, updated, opts.OverwriteDNS); err != nil {
			return nil, err
		}
	case tunnelManagementRemote:
		printRemoteAddHostnameSteps(u, hostname)
	default:
		return nil, fmt.Errorf("unsupported tunnel management mode %q", st.Management())
	}

	// Storefront `/` route must list every hostname or the new domain's `/`
	// falls through to the local frontend catch-all.
	if err := CreateStorefront(cfg, updated...); err != nil {
		u.Warnf("could not update storefront for new hostname: %v", err)
	}

	st.Hostnames = updated
	st.Hostname = updated[0]
	if err := saveTunnelState(cfg, st); err != nil {
		return nil, fmt.Errorf("hostname added in-cluster, but failed to persist state: %w", err)
	}

	u.Blank()
	u.Successf("Hostname added: https://%s", hostname)
	u.Dim("  Existing hostnames keep serving — this did not disrupt them.")
	u.Dim("  Verify: obol tunnel hostname list")

	return &HostnameMutationResult{
		Hostname:       hostname,
		Action:         "added",
		ManagementMode: st.Management(),
		Hostnames:      hostnameInfos(updated),
	}, nil
}

func addHostnameLocal(cfg *config.Config, u *ui.UI, kubeconfigPath string, st *tunnelState, hostname string, updated []string, overwriteDNS bool) error {
	if st.TunnelID == "" {
		return errors.New("local tunnel is missing its tunnel id; re-run 'obol tunnel setup --management local --hostname <host>'")
	}

	cloudflaredPath, err := exec.LookPath("cloudflared")
	if err != nil {
		return errors.New("cloudflared not found in PATH. Install it first (e.g. 'brew install cloudflared' on macOS)")
	}

	tunnelName := st.TunnelName
	if strings.TrimSpace(tunnelName) == "" {
		tunnelName = st.TunnelID // cloudflared accepts the UUID as the tunnel ref
	}

	u.Infof("Creating DNS route for %s...", hostname)
	routeArgs := routeDNSArgs(tunnelName, hostname, overwriteDNS)
	routeOut, err := exec.Command(cloudflaredPath, routeArgs...).CombinedOutput()
	if err != nil {
		hint := ""
		if !overwriteDNS && strings.Contains(string(routeOut), "record with that host already exists") {
			hint = "\nhint: a record for this hostname already exists. Re-run with --overwrite-dns to replace it."
		}
		return fmt.Errorf("cloudflared tunnel route dns failed: %w\n%s%s", err, strings.TrimSpace(string(routeOut)), hint)
	}
	if err := verifyRoutedHostname(string(routeOut), hostname); err != nil {
		return err
	}

	// Re-render the local config ConfigMap with one ingress rule per hostname,
	// then reload the connector (helm upgrade alone does not roll the pods on a
	// ConfigMap-only change, so the connector would keep its old ingress).
	cfgYAML := buildLocalManagedConfigYAML(updated, st.TunnelID)
	if err := kubectlApply(cfg, u, kubeconfigPath, cfgYAML); err != nil {
		return err
	}
	if err := helmUpgradeCloudflared(cfg, u, kubeconfigPath); err != nil {
		return err
	}
	return restartCloudflaredConnector(cfg, u, kubeconfigPath)
}

// RemoveHostname removes a public hostname from a persistent tunnel while every
// other hostname keeps serving. It refuses to remove the last hostname. It
// re-renders the ingress over the remaining set (local-managed) and updates the
// storefront route. DNS cleanup is intentionally NOT automated: under the
// least-privilege model obol holds no account-wide API token, so the operator
// removes the now-dangling CNAME in the Cloudflare dashboard.
func RemoveHostname(cfg *config.Config, u *ui.UI, opts RemoveHostnameOptions) (*HostnameMutationResult, error) {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		return nil, errors.New("hostname is required")
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return nil, errors.New("stack not running, use 'obol stack up' first")
	}

	st, err := loadTunnelState(cfg)
	if err != nil {
		return nil, fmt.Errorf("load tunnel state: %w", err)
	}
	if st == nil || !st.IsPersistent() {
		return nil, errors.New("no permanent tunnel configured; nothing to remove")
	}

	existing := st.HostnameSet()
	found := false
	remaining := make([]string, 0, len(existing))
	for _, h := range existing {
		if h == hostname {
			found = true
			continue
		}
		remaining = append(remaining, h)
	}
	if !found {
		return nil, fmt.Errorf("%s is not a tracked tunnel hostname (see 'obol tunnel hostname list')", hostname)
	}
	if len(remaining) == 0 {
		return nil, errors.New("refusing to remove the last hostname; a persistent tunnel needs at least one. " +
			"Tear the tunnel down with 'obol tunnel stop', or reconfigure with 'obol tunnel setup'")
	}

	switch st.Management() {
	case tunnelManagementLocal:
		// Re-render ingress over the remaining hostnames and reload the connector.
		cfgYAML := buildLocalManagedConfigYAML(remaining, st.TunnelID)
		if err := kubectlApply(cfg, u, kubeconfigPath, cfgYAML); err != nil {
			return nil, err
		}
		if err := helmUpgradeCloudflared(cfg, u, kubeconfigPath); err != nil {
			return nil, err
		}
		if err := restartCloudflaredConnector(cfg, u, kubeconfigPath); err != nil {
			return nil, err
		}
		printLocalRemoveDNSHint(u, hostname)
	case tunnelManagementRemote:
		printRemoteRemoveHostnameSteps(u, hostname)
	default:
		return nil, fmt.Errorf("unsupported tunnel management mode %q", st.Management())
	}

	// Storefront route now lists only the remaining hostnames.
	if err := CreateStorefront(cfg, remaining...); err != nil {
		u.Warnf("could not update storefront after removal: %v", err)
	}

	st.Hostnames = remaining
	st.Hostname = remaining[0]
	if err := saveTunnelState(cfg, st); err != nil {
		return nil, fmt.Errorf("hostname removed in-cluster, but failed to persist state: %w", err)
	}

	u.Blank()
	u.Successf("Hostname removed: %s", hostname)
	u.Dim(fmt.Sprintf("  Still serving: %s", strings.Join(remaining, ", ")))

	return &HostnameMutationResult{
		Hostname:       hostname,
		Action:         "removed",
		ManagementMode: st.Management(),
		Hostnames:      hostnameInfos(remaining),
	}, nil
}

// restartCloudflaredConnector rolls the cloudflared Deployment so the connector
// reloads its ingress ConfigMap. Adding or removing a hostname changes only the
// ConfigMap, not the Deployment pod spec, so `helm upgrade` does not restart the
// pods and the running connector keeps its old ingress — a newly added hostname
// 404s at the connector (its rule isn't loaded) and a removed hostname keeps
// serving. Rolling the Deployment forces the new config to load.
func restartCloudflaredConnector(cfg *config.Config, u *ui.UI, kubeconfigPath string) error {
	kubectlPath := filepath.Join(cfg.BinDir, "kubectl")
	u.Dim("Reloading tunnel connector...")
	if out, err := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath,
		"rollout", "restart", "deployment/cloudflared", "-n", tunnelNamespace).CombinedOutput(); err != nil {
		return fmt.Errorf("restart cloudflared connector: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command(kubectlPath, "--kubeconfig", kubeconfigPath,
		"rollout", "status", "deployment/cloudflared", "-n", tunnelNamespace, "--timeout=120s").CombinedOutput(); err != nil {
		return fmt.Errorf("wait for cloudflared restart: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func hostnameInfos(hosts []string) []HostnameInfo {
	infos := make([]HostnameInfo, 0, len(hosts))
	for i, h := range hosts {
		infos = append(infos, HostnameInfo{
			Hostname: h,
			Primary:  i == 0,
			URL:      "https://" + h,
		})
	}
	return infos
}

// printLocalRemoveDNSHint explains that the hostname has stopped serving (its
// ingress rule is gone) but its CNAME must be removed in the dashboard, because
// obol intentionally holds no account-wide API token to delete DNS records.
func printLocalRemoveDNSHint(u *ui.UI, hostname string) {
	u.Blank()
	u.Dim(fmt.Sprintf("%s no longer routes to any service (its ingress rule was removed).", hostname))
	u.Dim("  Its CNAME still resolves — delete it in the Cloudflare dashboard (DNS →")
	u.Dim("  Records). Obol holds no account-wide API token by design, so it does not")
	u.Dim("  delete DNS records for you.")
}

func printRemoteAddHostnameSteps(u *ui.UI, hostname string) {
	u.Blank()
	u.Bold("Add this hostname in the Cloudflare dashboard")
	u.Print("Your tunnel is dashboard-managed (connector token, least privilege), so Obol")
	u.Print("does not hold an API token to edit its ingress. Finish in the dashboard:")
	u.Print("  1. https://one.dash.cloudflare.com → Networks → Tunnels → your tunnel.")
	u.Print("  2. Public Hostname tab → Add a public hostname:")
	u.Detail("       Subdomain / Domain", hostname)
	u.Detail("       Type", "HTTP")
	u.Detail("       Service URL", "http://traefik.traefik.svc.cluster.local:80")
	u.Print("  3. Save. Obol has already updated the in-cluster storefront route.")
	u.Blank()
}

func printRemoteRemoveHostnameSteps(u *ui.UI, hostname string) {
	u.Blank()
	u.Bold("Remove this hostname in the Cloudflare dashboard")
	u.Print("Your tunnel is dashboard-managed, so its ingress lives in Cloudflare:")
	u.Print("  1. https://one.dash.cloudflare.com → Networks → Tunnels → your tunnel.")
	u.Printf("  2. Public Hostname tab → delete the entry for %s.", hostname)
	u.Print("  3. Save. Obol has already updated the in-cluster storefront route.")
	u.Blank()
}
