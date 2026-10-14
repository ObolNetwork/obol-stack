package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/tools"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/update"
	"github.com/ObolNetwork/obol-stack/internal/version"
)

// ClusterCheckID runs first; checks with NeedsCluster wait on its result.
const ClusterCheckID = "cluster"

const gib = 1 << 30

// Checks returns every check in display order. repairs supplies the fixes
// that need UI or side-effecting packages the doctor package doesn't import.
func Checks(r Repairs) []Check {
	return []Check{
		{ID: "docker", Group: GroupHost, Title: "Docker", Run: checkDocker},
		{ID: "disk", Group: GroupHost, Title: "Disk space", Run: checkDisk},
		{ID: "hosts", Group: GroupHost, Title: "obol.stack name", Run: checkHostsEntry},
		{ID: "ports", Group: GroupHost, Title: "Ingress ports", Run: checkPorts},
		{ID: "obol", Group: GroupTools, Title: "obol version", Online: true, Run: checkObolVersion},
		{ID: "tools", Group: GroupTools, Title: "Pinned tools", Run: checkTools, Repair: r.InstallTools},
		{ID: ClusterCheckID, Group: GroupStack, Title: "Cluster", Run: checkCluster, Repair: r.RefreshKubeconfig},
		{ID: "nodes", Group: GroupStack, Title: "Node", NeedsCluster: true, Run: checkNodes},
		{ID: "pods", Group: GroupStack, Title: "Pods", NeedsCluster: true, Run: checkPods},
		{ID: "releases", Group: GroupStack, Title: "Helm releases", NeedsCluster: true, Run: checkReleases},
		{ID: "models", Group: GroupServices, Title: "Models", NeedsCluster: true, Run: checkModels},
		{ID: "litellm-drift", Group: GroupServices, Title: "LiteLLM router", NeedsCluster: true, Run: checkRouterDrift},
		{ID: "ollama", Group: GroupServices, Title: "Ollama", Run: checkOllama},
		{ID: "ca-bundle", Group: GroupServices, Title: "x402 CA bundle", NeedsCluster: true, Run: checkCABundle, Repair: r.PopulateCABundle},
		{ID: "tunnel", Group: GroupTunnel, Title: "Cloudflare tunnel", NeedsCluster: true, Run: checkTunnel},
		{ID: "wallets", Group: GroupWallets, Title: "Agent wallets", Run: checkWallets},
	}
}

// Repairs are the safe, idempotent fixes --fix may run. Nil entries mean
// the check reports a command instead.
type Repairs struct {
	InstallTools      func(ctx context.Context, e *Env) error
	RefreshKubeconfig func(ctx context.Context, e *Env) error
	PopulateCABundle  func(ctx context.Context, e *Env) error
}

// --- Host ---------------------------------------------------------------

type dockerInfo struct {
	ServerVersion   string `json:"ServerVersion"`
	OperatingSystem string `json:"OperatingSystem"`
	MemTotal        int64  `json:"MemTotal"`
	NCPU            int    `json:"NCPU"`
}

func checkDocker(ctx context.Context, e *Env) Result {
	out, err := e.Run(ctx, "docker", "info", "--format", "{{json .}}")
	if err != nil {
		return fail("Docker isn't reachable", dockerStartHint())
	}

	var d dockerInfo
	if err := json.Unmarshal(out, &d); err != nil || d.ServerVersion == "" {
		return fail("Docker answered but its info is unreadable", dockerStartHint())
	}

	mem := float64(d.MemTotal) / gib
	msg := fmt.Sprintf("%s %s, %.1f GiB memory, %d CPUs", engineName(d.OperatingSystem), d.ServerVersion, mem, d.NCPU)

	// The stack itself needs ~3–4 GiB before local models or chain nodes.
	switch {
	case mem < 3:
		return fail(msg+": too little memory for the stack", "give Docker at least 6 GiB ("+memorySetting(d.OperatingSystem)+")")
	case mem < 6:
		return warn(msg+": tight once agents are busy", "give Docker 6 GiB or more ("+memorySetting(d.OperatingSystem)+")")
	default:
		return ok(msg)
	}
}

func engineName(os string) string {
	switch {
	case strings.Contains(os, "Docker Desktop"):
		return "Docker Desktop"
	case strings.Contains(strings.ToLower(os), "orbstack"):
		return "OrbStack"
	default:
		return "Docker"
	}
}

func memorySetting(os string) string {
	if strings.Contains(os, "Docker Desktop") {
		return "Docker Desktop → Settings → Resources → Memory"
	}

	if strings.Contains(strings.ToLower(os), "orbstack") {
		return "OrbStack → Settings → System → Memory limit"
	}

	return "your Docker VM's memory setting"
}

func dockerStartHint() string {
	if runtime.GOOS == "darwin" {
		return "start Docker Desktop (or OrbStack), then re-run obol doctor"
	}

	return "sudo systemctl start docker (and add yourself to the docker group)"
}

func checkDisk(ctx context.Context, e *Env) Result {
	dir := existingParent(e.Cfg.DataDir)

	free, err := e.FreeBytes(dir)
	if err != nil {
		return skip("can't read free space: " + err.Error())
	}

	msg := fmt.Sprintf("%.0f GiB free for %s", float64(free)/gib, dir)

	switch {
	case free < 2*gib:
		return fail(msg, "free up disk space; agents and the cluster stop writing when it's full")
	case free < 15*gib:
		return warn(msg, "free up disk space before installing chain nodes or pulling models")
	default:
		return ok(msg)
	}
}

func existingParent(path string) string {
	for p := path; p != "" && p != string(filepath.Separator); p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return string(filepath.Separator)
}

func checkHostsEntry(ctx context.Context, e *Env) Result {
	addrs, err := e.LookupHost(ctx, "obol.stack")
	if err == nil && (slices.Contains(addrs, "127.0.0.1") || slices.Contains(addrs, "::1")) {
		return ok("obol.stack → 127.0.0.1")
	}

	got := "doesn't resolve"
	if err == nil {
		got = "resolves to " + strings.Join(addrs, ", ")
	}

	return fail("obol.stack "+got+"; the dashboard and local routes need it on this machine",
		`sudo sh -c 'echo "127.0.0.1 obol.stack" >> /etc/hosts'  (obol stack up also offers to add it)`)
}

// checkPorts: with the stack up, the ingress must answer on :8080; with it
// down, the ports it binds must be free so `obol stack up` can start.
func checkPorts(ctx context.Context, e *Env) Result {
	if e.ClusterUp() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8080/", nil)
		req.Host = "obol.stack"

		resp, err := e.HTTP.Do(req)
		if err != nil {
			return fail("ingress isn't answering on :8080", "obol stack up  (re-creates the k3d load balancer if it's gone)")
		}

		_ = resp.Body.Close()

		return ok(fmt.Sprintf("ingress answering on :8080 (HTTP %d)", resp.StatusCode))
	}

	ports := []string{"8080", "8443"}
	if runtime.GOOS == "darwin" || os.Geteuid() == 0 {
		ports = append([]string{"80", "443"}, ports...) // binding <1024 needs root elsewhere
	}

	var busy []string

	for _, p := range ports {
		if err := e.Listen(net.JoinHostPort("127.0.0.1", p)); err != nil {
			busy = append(busy, ":"+p)
		}
	}

	if len(busy) > 0 {
		return fail(strings.Join(busy, ", ")+" already in use; obol stack up needs them",
			"find the owner with: lsof -nP -iTCP"+busy[0]+" -sTCP:LISTEN")
	}

	return ok(strings.Join(ports, ", ") + " free")
}

// --- Tools --------------------------------------------------------------

func checkObolVersion(ctx context.Context, e *Env) Result {
	current := version.Version
	if current == "dev" || current == "" {
		return info("development build")
	}

	latest, err := update.CheckLatestRelease()
	if err != nil || latest == nil {
		return skip("couldn't reach GitHub to compare")
	}

	if tools.CompareVersions(strings.TrimPrefix(current, "v"), latest.Version) >= 0 {
		return ok(current + " (latest)")
	}

	method := update.CurrentInstallMethod(e.Cfg.BinDir)

	return warn(fmt.Sprintf("%s installed, %s available", current, latest.TagName),
		update.CLIUpgradeCommand(method, latest.TagName))
}

func checkTools(ctx context.Context, e *Env) Result {
	statuses := tools.NewInstaller(e.Cfg.BinDir).Status()

	var missing, outdated, fine []string

	for _, s := range statuses {
		switch {
		case s.State == tools.StateMissing && s.Required:
			missing = append(missing, s.Name)
		case s.State == tools.StateOutdated && s.Source == tools.SourceManaged:
			outdated = append(outdated, fmt.Sprintf("%s %s→%s", s.Name, s.Installed, s.Wanted))
		case s.State != tools.StateMissing:
			fine = append(fine, s.Name)
		}
	}

	switch {
	case len(missing) > 0:
		return fixable(fail("missing: "+strings.Join(missing, ", "), "obol upgrade --tools-only"))
	case len(outdated) > 0:
		return warn("update available: "+strings.Join(outdated, ", "), "obol upgrade --tools-only")
	default:
		return ok(strings.Join(fine, ", ") + " at pinned versions")
	}
}

// --- Stack --------------------------------------------------------------

func checkCluster(ctx context.Context, e *Env) Result {
	id := readTrim(filepath.Join(e.Cfg.ConfigDir, ".stack-id"))
	if id == "" {
		return fail("no stack here ("+e.Cfg.ConfigDir+")", "obol stack init && obol stack up")
	}

	if _, err := e.Kubectl(ctx, "--request-timeout=5s", "get", "--raw", "/readyz"); err == nil {
		return ok("stack " + id + " running")
	}

	backend := readTrim(filepath.Join(e.Cfg.ConfigDir, ".stack-backend"))
	if backend != "" && backend != "k3d" {
		return warn("stack "+id+" isn't answering ("+backend+" backend)", "obol stack up")
	}

	running, exists := k3dClusterState(ctx, e, "obol-stack-"+id)

	switch {
	case running:
		return fixable(warn("stack "+id+" is running but the kubeconfig points at an old API port", "obol doctor --fix  (or obol stack up)"))
	case exists:
		return warn("stack "+id+" is stopped", "obol stack up")
	default:
		return fail("stack "+id+" has no cluster", "obol stack up")
	}
}

type k3dCluster struct {
	Name           string `json:"name"`
	ServersRunning int    `json:"serversRunning"`
}

func k3dClusterState(ctx context.Context, e *Env, name string) (running, exists bool) {
	out, err := e.Run(ctx, e.Cfg.ToolPath("k3d"), "cluster", "list", "-o", "json")
	if err != nil {
		return false, false
	}

	var clusters []k3dCluster
	if json.Unmarshal(out, &clusters) != nil {
		return false, false
	}

	for _, c := range clusters {
		if c.Name == name {
			return c.ServersRunning > 0, true
		}
	}

	return false, false
}

type nodeList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

func checkNodes(ctx context.Context, e *Env) Result {
	out, err := e.Kubectl(ctx, "get", "nodes", "-o", "json")
	if err != nil {
		return warn("couldn't list nodes", "obol kubectl get nodes")
	}

	var nl nodeList
	if err := json.Unmarshal(out, &nl); err != nil || len(nl.Items) == 0 {
		return warn("no nodes reported", "obol kubectl get nodes")
	}

	var problems []string

	for _, n := range nl.Items {
		for _, c := range n.Status.Conditions {
			switch {
			case c.Type == "Ready" && c.Status != "True":
				problems = append(problems, n.Metadata.Name+" not Ready")
			case c.Type != "Ready" && strings.HasSuffix(c.Type, "Pressure") && c.Status == "True":
				problems = append(problems, n.Metadata.Name+" "+c.Type)
			}
		}
	}

	if len(problems) > 0 {
		return fail(strings.Join(problems, "; "), "free memory/disk for Docker, then: obol kubectl describe node")
	}

	return ok(fmt.Sprintf("%d node(s) Ready, no resource pressure", len(nl.Items)))
}

type podList struct {
	Items []pod `json:"items"`
}

type pod struct {
	Metadata struct {
		Name              string    `json:"name"`
		Namespace         string    `json:"namespace"`
		CreationTimestamp time.Time `json:"creationTimestamp"`
	} `json:"metadata"`
	Status struct {
		Phase             string            `json:"phase"`
		ContainerStatuses []containerStatus `json:"containerStatuses"`
	} `json:"status"`
}

type containerStatus struct {
	Ready        bool `json:"ready"`
	RestartCount int  `json:"restartCount"`
	State        struct {
		Waiting *struct {
			Reason string `json:"reason"`
		} `json:"waiting"`
	} `json:"state"`
}

// badWaiting reasons never resolve on their own.
var badWaiting = []string{"CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError", "InvalidImageName"}

func checkPods(ctx context.Context, e *Env) Result {
	out, err := e.Kubectl(ctx, "get", "pods", "-A", "-o", "json")
	if err != nil {
		return warn("couldn't list pods", "obol kubectl get pods -A")
	}

	var pl podList
	if err := json.Unmarshal(out, &pl); err != nil {
		return warn("couldn't read pod list", "obol kubectl get pods -A")
	}

	problems, flapping := podProblems(pl.Items, time.Now())

	switch {
	case len(problems) > 0:
		first := problems[0]

		return fail(fmt.Sprintf("%d unhealthy: %s", len(problems), joinMax(problems, 4)),
			fmt.Sprintf("obol kubectl describe pod -n %s %s  (and: obol kubectl logs -n %s %s)", first.ns, first.name, first.ns, first.name))
	case len(flapping) > 0:
		return warn("restarting often: "+joinMax(flapping, 4), "obol kubectl logs --previous -n <namespace> <pod>")
	default:
		return ok(fmt.Sprintf("%d pods healthy", len(pl.Items)))
	}
}

type podIssue struct{ ns, name, reason string }

func (p podIssue) String() string { return p.ns + "/" + p.name + " (" + p.reason + ")" }

func podProblems(pods []pod, now time.Time) (problems []podIssue, flapping []podIssue) {
	for _, p := range pods {
		age := now.Sub(p.Metadata.CreationTimestamp)
		issue := podIssue{ns: p.Metadata.Namespace, name: p.Metadata.Name}

		if p.Status.Phase == "Succeeded" {
			continue
		}

		reason := ""
		restarts := 0
		ready := len(p.Status.ContainerStatuses) > 0

		for _, c := range p.Status.ContainerStatuses {
			restarts += c.RestartCount
			ready = ready && c.Ready

			if c.State.Waiting != nil && slices.Contains(badWaiting, c.State.Waiting.Reason) {
				reason = c.State.Waiting.Reason
			}
		}

		switch {
		case reason != "":
			issue.reason = reason
		case p.Status.Phase == "Failed" || p.Status.Phase == "Unknown":
			issue.reason = p.Status.Phase
		case p.Status.Phase == "Pending" && age > 3*time.Minute:
			issue.reason = "Pending " + age.Truncate(time.Minute).String()
		case p.Status.Phase == "Running" && !ready && age > 5*time.Minute:
			issue.reason = "not ready"
		}

		if issue.reason != "" {
			problems = append(problems, issue)
		} else if restarts >= 5 {
			issue.reason = fmt.Sprintf("%d restarts", restarts)
			flapping = append(flapping, issue)
		}
	}

	return problems, flapping
}

func joinMax[T fmt.Stringer](items []T, n int) string {
	parts := make([]string, 0, n)
	for i, it := range items {
		if i == n {
			parts = append(parts, fmt.Sprintf("+%d more", len(items)-n))
			break
		}

		parts = append(parts, it.String())
	}

	return strings.Join(parts, ", ")
}

type helmRelease struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
}

func checkReleases(ctx context.Context, e *Env) Result {
	out, err := e.Run(ctx, e.Cfg.ToolPath("helm"), "--kubeconfig", e.kubeconfig(), "list", "-A", "-o", "json")
	if err != nil {
		return warn("couldn't list Helm releases", "obol helm list -A")
	}

	var rels []helmRelease
	if err := json.Unmarshal(out, &rels); err != nil {
		return warn("couldn't read Helm releases", "obol helm list -A")
	}

	var bad []string

	for _, r := range rels {
		if r.Status != "deployed" {
			bad = append(bad, fmt.Sprintf("%s/%s %s", r.Namespace, r.Name, r.Status))
		}
	}

	if len(bad) > 0 {
		return fail(strings.Join(bad, ", "), "obol stack up  (re-syncs every release)")
	}

	return ok(fmt.Sprintf("%d deployed", len(rels)))
}

// --- Services -----------------------------------------------------------

func checkModels(ctx context.Context, e *Env) Result {
	names, err := model.GetConfiguredModels(e.Cfg)
	if err != nil {
		return warn("couldn't read the LiteLLM model list", "obol model status")
	}

	var usable []string

	for _, n := range names {
		if !strings.HasPrefix(n, "paid/") {
			usable = append(usable, n)
		}
	}

	if len(usable) == 0 {
		return fail("no chat model configured; agents can't answer", "obol model setup")
	}

	return ok(fmt.Sprintf("%d configured, default %s", len(usable), usable[0]))
}

func checkRouterDrift(ctx context.Context, e *Env) Result {
	drift, err := model.CheckRouterDrift(e.Cfg)
	if err != nil {
		return skip("couldn't compare: " + err.Error())
	}

	if drift.Empty() {
		return ok("live router matches the config")
	}

	var parts []string
	if len(drift.Missing) > 0 {
		parts = append(parts, "not served: "+strings.Join(drift.Missing, ", "))
	}

	if len(drift.Extra) > 0 {
		parts = append(parts, "served but removed: "+strings.Join(drift.Extra, ", "))
	}

	return warn(strings.Join(parts, "; "), "obol kubectl rollout restart deployment/litellm -n llm")
}

func checkOllama(ctx context.Context, e *Env) Result {
	ms, err := model.ListOllamaModels()
	if err != nil {
		return info("not running (only needed for local models)")
	}

	if r, exposed := ollamaListensOnAllInterfaces(ctx, e); exposed {
		return r
	}

	return info(fmt.Sprintf("running, %d model(s) pulled", len(ms)))
}

// ollamaGOOS is swappable for tests.
var ollamaGOOS = runtime.GOOS

// ollamaListensOnAllInterfaces flags a Linux Ollama bound to every interface
// (OLLAMA_HOST=0.0.0.0, needed so the k3d cluster can reach it over the
// Docker bridge). Ollama has no authentication, so that also serves anyone
// on the local network unless a firewall limits the port.
func ollamaListensOnAllInterfaces(ctx context.Context, e *Env) (Result, bool) {
	if ollamaGOOS != "linux" {
		return Result{}, false
	}

	out, err := e.Run(ctx, "ss", "-Hltn", "sport = :11434")
	if err != nil {
		return Result{}, false
	}

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		switch fields[3] {
		case "0.0.0.0:11434", "*:11434", "[::]:11434":
			return warn("listening on all interfaces, so anyone on your network can use it (Ollama has no authentication)",
				"limit port 11434 to Docker networks, e.g.: sudo ufw allow from 172.16.0.0/12 to any port 11434 proto tcp && sudo ufw deny 11434/tcp"), true
		}
	}

	return Result{}, false
}

func checkCABundle(ctx context.Context, e *Env) Result {
	out, err := e.Kubectl(ctx, "get", "configmap", "ca-certificates", "-n", "x402", "-o", `jsonpath={.data.ca-certificates\.crt}`)
	if err != nil || len(out) < 1024 {
		return fixable(fail("empty, so the x402 verifier can't reach the facilitator over TLS", "obol doctor --fix  (or obol stack up)"))
	}

	return ok(fmt.Sprintf("%d KiB of CAs", len(out)/1024))
}

// --- Tunnel -------------------------------------------------------------

func checkTunnel(ctx context.Context, e *Env) Result {
	rep, running, err := tunnel.Report(e.Cfg, e.Offline)
	if err != nil {
		return skip(err.Error())
	}

	where := rep.URL
	if rep.Hostname != "" {
		where = rep.Hostname
	}

	switch {
	case rep.Status == "dormant":
		return ok("quick tunnel dormant (starts on the first obol sell)")
	case !running:
		return fail("persistent tunnel "+where+" isn't running", "obol tunnel restart")
	case rep.TokenRejected:
		return fail("Cloudflare rejects the connector token (tunnel deleted or token rotated)",
			"copy the tunnel's token from the Cloudflare dashboard, then: obol tunnel setup <token> --hostname "+fallback(rep.Hostname, "<hostname>"))
	case rep.PublicHTTPStatus == 525:
		return fail(where+" returns 525: Cloudflare speaks TLS to a plain-HTTP origin",
			"in the dashboard, set the Public Hostname's Service to http://traefik.traefik.svc.cluster.local:80")
	case rep.Status == "active":
		msg := "active: " + where
		if rep.ActiveConnections > 0 {
			msg += fmt.Sprintf(" (%d connections)", rep.ActiveConnections)
		}

		return ok(msg)
	default:
		return warn(rep.Status+": "+where, "obol tunnel status --verbose; obol tunnel logs")
	}
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}

	return s
}

// --- Wallets ------------------------------------------------------------

// checkWallets lists agent wallet addresses (public) so the operator knows
// what a backup must cover. Only each keystore's "address" field is read.
func checkWallets(ctx context.Context, e *Env) Result {
	matches, _ := filepath.Glob(filepath.Join(e.Cfg.DataDir, "*", "remote-signer-keystores", "*.json"))

	var addrs []string

	for _, m := range matches {
		if a := keystoreAddress(m); a != "" {
			ns := filepath.Base(filepath.Dir(filepath.Dir(m)))
			addrs = append(addrs, fmt.Sprintf("%s %s", strings.TrimPrefix(ns, "hermes-"), shortAddr(a)))
		}
	}

	subAgents := 0

	if e.ClusterUp() {
		if out, err := e.Kubectl(ctx, "get", "agents.obol.org", "-A", "-o", "jsonpath={.items[*].status.walletAddress}"); err == nil {
			subAgents = len(strings.Fields(string(out)))
		}
	}

	if len(addrs) == 0 && subAgents == 0 {
		return info("none yet")
	}

	msg := strings.Join(addrs, ", ")
	if subAgents > 0 {
		msg = strings.TrimPrefix(msg+fmt.Sprintf(", %d sub-agent wallet(s) held only in the cluster", subAgents), ", ")
	}

	r := info(msg)
	r.Fix = "back up: obol agent wallet backup --file <path>; sub-agents: obol stack export --file <path> (stack running)"

	return r
}

func keystoreAddress(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var ks struct {
		Address string `json:"address"`
	}

	if json.NewDecoder(f).Decode(&ks) != nil || ks.Address == "" {
		return ""
	}

	return "0x" + strings.TrimPrefix(ks.Address, "0x")
}

func shortAddr(a string) string {
	if len(a) < 12 {
		return a
	}

	return a[:6] + "…" + a[len(a)-4:]
}

func readTrim(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}
