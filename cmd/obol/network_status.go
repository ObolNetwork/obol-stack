package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

// statusCommandName is the name of every "status" subcommand.
const statusCommandName = "status"

// exitUnhealthy is `network status`'s exit code when a selected node is
// degraded, unreachable or not deployed.
const exitUnhealthy = 3

// statusBudget bounds one round of probes (port-forwards + API calls).
const statusBudget = 20 * time.Second

func networkStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      statusCommandName,
		Usage:     "Show the RPC gateway and the live sync state of installed local nodes",
		ArgsUsage: deploymentArgsUsage,
		Description: `Shows the RPC gateway (eRPC): every chain it routes, its upstreams (local
nodes marked and linked to their section) and which upstream answers right
now (one eth_blockNumber per chain through eRPC, read from its
X-Erpc-Upstreams header). Then, per installed ethereum node: a verdict, the
execution client's sync stages (reth's staged sync: Headers, Bodies,
Execution, ...), the consensus client's stages (finalized sync, head sync,
backfill), pod problems with a hint, data size and a Grafana link.

Node and eRPC APIs are reached through kubectl port-forwards on the stack
kubeconfig; nothing is exposed on obol.stack.

--watch redraws every --interval, keeps the port-forwards open and adds
rates and ETAs per stage. With -o json --watch it prints one JSON object
per line (NDJSON) per tick. Ctrl-C exits.

Exit status: 0, or 3 when any selected node is degraded, el_offline,
unreachable or not_deployed (stopped and syncing nodes count as fine), so
"obol network status ethereum/mainnet && ..." is scriptable. --no-probe
and --watch always exit 0.

Examples:
  obol network status
  obol network status ethereum/mainnet --watch
  obol -o json network status ethereum/hoodi
  obol -o json network status --watch --interval 10s`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "no-probe",
				Usage: "Skip live API probes (node APIs and the eRPC serving check); show config and pods only",
			},
			&cli.BoolFlag{
				Name:    "watch",
				Aliases: []string{"w"},
				Usage:   "Refresh until Ctrl-C, with sync rates and ETAs (NDJSON with -o json)",
			},
			&cli.DurationFlag{
				Name:  "interval",
				Value: 5 * time.Second,
				Usage: "Refresh interval for --watch",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			arg := cmd.Args().First()

			installed := network.InstalledDeployments(cfg)

			deployments, err := selectDeployments(installed, arg)
			if err != nil {
				return err
			}

			watch := cmd.Bool("watch")
			if watch && !u.IsJSON() && !u.IsTTY() {
				return errors.New("--watch needs a terminal; use -o json --watch for one JSON object per refresh, or drop --watch")
			}

			interval := cmd.Duration("interval")
			if interval < time.Second {
				return fmt.Errorf("--interval must be at least 1s (got %s)", interval)
			}

			if err := kubectl.EnsureCluster(cfg); err != nil {
				return fmt.Errorf("failed to get eRPC status: %w", err)
			}

			prober := network.NewKubectlNodeProber(cfg)
			opts := network.StatusOptions{NoProbe: cmd.Bool("no-probe"), Installed: installed}

			if watch {
				reselect := func() []network.Deployment {
					all := network.InstalledDeployments(cfg)
					if sel, err := selectDeployments(all, arg); err == nil {
						opts.Installed = all
						return sel
					}

					return deployments
				}

				return watchStatus(ctx, u, prober, reselect, &opts, interval, os.Stdout, terminalWidth)
			}

			probeCtx, cancel := context.WithTimeout(ctx, statusBudget)
			report := prober.Gather(probeCtx, deployments, opts)
			cancel()
			prober.Close()

			fillUpstreamCounts(&report)

			if u.IsJSON() {
				if err := u.JSON(report); err != nil {
					return err
				}
			} else {
				u.Print(strings.TrimRight(renderStatus(report, renderOptions{Verbose: u.IsVerbose(), NoProbe: opts.NoProbe}), "\n"))
			}

			if !opts.NoProbe {
				if bad := report.Unhealthy(); len(bad) > 0 {
					return cli.Exit("", exitUnhealthy)
				}
			}

			return nil
		},
	}
}

// fillUpstreamCounts keeps the v0.16 erpc.upstreams JSON (per-chain counts
// with a display name) alongside erpc.chains.
func fillUpstreamCounts(r *network.StatusReport) {
	r.ERPC.Upstreams = []network.ERPCChainCount{}
	for _, c := range r.ERPC.Chains {
		r.ERPC.Upstreams = append(r.ERPC.Upstreams, network.ERPCChainCount{
			ChainID: c.ChainID, Name: chainIDToName(c.ChainID), Upstreams: len(c.Upstreams),
		})
	}
}

// selectDeployments narrows deployments to the one named by arg: an exact
// "<type>/<id>", its namespace "<type>-<id>", or a type with exactly one
// deployment. An empty arg selects all.
func selectDeployments(all []network.Deployment, arg string) ([]network.Deployment, error) {
	if arg == "" {
		return all, nil
	}

	var byType []network.Deployment
	for _, d := range all {
		if arg == d.Identifier() || arg == d.Namespace {
			return []network.Deployment{d}, nil
		}
		if arg == d.Type {
			byType = append(byType, d)
		}
	}

	if len(byType) == 1 {
		return byType, nil
	}

	ids := make([]string, 0, len(all))
	for _, d := range all {
		ids = append(ids, d.Identifier())
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("no network deployment %q: none installed", arg)
	}

	return nil, fmt.Errorf("no single network deployment matches %q; installed: %s", arg, strings.Join(ids, ", "))
}

// ---------------------------------------------------------------------------
// --watch
// ---------------------------------------------------------------------------

func terminalWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}

	return 0
}

// watchStatus refreshes the report every interval until ctx ends or the
// user hits Ctrl-C. Human output redraws in place (cursor up + clear);
// JSON output appends one compact object per tick (NDJSON). Forwards stay
// open for the whole session and are closed on exit.
func watchStatus(ctx context.Context, u *ui.UI, prober *network.NodeProber, deployments func() []network.Deployment,
	opts *network.StatusOptions, interval time.Duration, out io.Writer, width func() int,
) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	prober.StartSession(ctx)
	defer prober.Close()

	tracker := network.NewRateTracker()
	enc := json.NewEncoder(out)
	human := !u.IsJSON()
	drawn := 0

	if human {
		fmt.Fprint(out, "\x1b[?25l") // hide the cursor while redrawing
		defer fmt.Fprint(out, "\x1b[?25h")
	}

	for {
		tickCtx, cancel := context.WithTimeout(ctx, statusBudget)
		report := prober.Gather(tickCtx, deployments(), *opts)
		cancel()

		if ctx.Err() != nil {
			break
		}

		tracker.Annotate(report.Nodes, report.Time)
		fillUpstreamCounts(&report)

		if human {
			frame := ui.Paint("dim", fmt.Sprintf("obol network status · every %s · %s · Ctrl-C to quit",
				interval, report.Time.Format("15:04:05"))) + "\n\n" +
				renderStatus(report, renderOptions{Verbose: u.IsVerbose(), NoProbe: opts.NoProbe, Watch: true})

			if drawn > 0 {
				fmt.Fprintf(out, "\x1b[%dA\r\x1b[J", drawn)
			}

			fmt.Fprint(out, frame)
			drawn = physicalLines(frame, width())
		} else if err := enc.Encode(report); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
		case <-time.After(interval):
			continue
		}

		break
	}

	return nil
}

// physicalLines counts the terminal rows a frame occupies, including lines
// that wrap at width (0 = unknown, no wrapping assumed).
func physicalLines(frame string, width int) int {
	lines := strings.Split(strings.TrimSuffix(frame, "\n"), "\n")

	n := 0
	for _, l := range lines {
		w := lipgloss.Width(l)
		if width > 0 && w > width {
			n += (w + width - 1) / width
		} else {
			n++
		}
	}

	return n
}

// ---------------------------------------------------------------------------
// rendering
// ---------------------------------------------------------------------------

type renderOptions struct {
	Verbose bool
	NoProbe bool
	Watch   bool
}

const (
	indent      = "    "
	stageIndent = "      "
)

// statusBuilder accumulates lines of a frame.
type statusBuilder struct{ strings.Builder }

func (b *statusBuilder) linef(format string, args ...any) {
	fmt.Fprintf(b, format, args...)
	b.WriteByte('\n')
}

func (b *statusBuilder) blank() { b.WriteByte('\n') }

// renderStatus renders the whole report as one string (so --watch can
// redraw it in place).
func renderStatus(r network.StatusReport, o renderOptions) string {
	var b statusBuilder

	nodes := map[string]network.NodeStatus{}
	for _, n := range r.Nodes {
		nodes[n.Identifier()] = n
	}

	renderERPC(&b, r.ERPC, nodes, o)

	for _, n := range r.Nodes {
		b.blank()
		renderNode(&b, n, r.ERPC.Chains, o)
	}

	if len(r.Nodes) == 0 {
		b.blank()
		b.linef("%s", ui.Paint("dim", "No local nodes installed (obol network install ethereum --help)"))
	}

	return b.String()
}

func renderERPC(b *statusBuilder, st network.ERPCStatus, nodes map[string]network.NodeStatus, o renderOptions) {
	health := map[string]string{
		network.ERPCHealthy:  ui.Paint("ok", "healthy"),
		network.ERPCDegraded: ui.Paint("warn", "degraded"),
		network.ERPCDown:     ui.Paint("fail", "not running"),
	}[st.Health]
	if health == "" {
		health = ui.Paint("dim", "unknown")
	}

	b.linef("%s", ui.Heading(fmt.Sprintf("RPC gateway (eRPC)  ·  %s/<network>  ·  %s", st.Endpoint, health)))
	b.linef("%s%s", indent, ui.Paint("dim", "Routes reads to the best upstream per chain; local nodes are tried first."))

	for _, p := range st.Pods {
		if o.Verbose {
			b.linef("%s%s", indent, ui.Paint("dim", erpcPodLine(p, true)))
		} else if !p.Ready {
			b.linef("%s%s eRPC %s", indent, ui.Paint("fail", ui.GlyphFail), erpcPodLine(p, false))
		}
	}

	if st.Error != "" {
		b.linef("%s%s Could not read the eRPC config: %s", indent, ui.Paint("fail", ui.GlyphFail), st.Error)
		return
	}

	if len(st.Chains) == 0 {
		b.linef("%s%s", indent, ui.Paint("dim", "(no upstreams configured — obol network add <chain>)"))
		return
	}

	type row struct {
		network, chain string
		ups            []string // plain text
		upsStyled      []string
		serving        string
		detail         string
	}

	rows := make([]row, 0, len(st.Chains))
	for _, c := range st.Chains {
		r := row{network: c.Network, chain: strconv.Itoa(c.ChainID)}

		var remotes []network.ERPCUpstream
		for _, up := range c.Upstreams {
			if !up.Local {
				remotes = append(remotes, up)
				continue
			}

			label := "local " + up.Deployment
			kind := "dim"
			if n, ok := nodes[up.Deployment]; ok {
				label += " (" + upstreamStateLabel(n.Verdict.State) + ")"
				kind = verdictKind(n.Verdict.State)
			}

			r.ups = append(r.ups, ui.GlyphBullet+" "+label)
			r.upsStyled = append(r.upsStyled, ui.Paint(kind, ui.GlyphBullet)+" "+label)
		}

		switch {
		case o.Verbose:
			for _, up := range remotes {
				r.ups = append(r.ups, ui.GlyphBullet+" "+up.ID)
				r.upsStyled = append(r.upsStyled, ui.Paint("dim", ui.GlyphBullet)+" "+up.ID)
			}
		case len(remotes) > 0:
			label := remotes[0].ID
			if len(remotes) > 1 {
				label += fmt.Sprintf(", +%d", len(remotes)-1)
			}

			r.ups = append(r.ups, ui.GlyphBullet+" "+label)
			r.upsStyled = append(r.upsStyled, ui.Paint("dim", ui.GlyphBullet)+" "+label)
		}

		if len(r.ups) == 0 {
			r.ups, r.upsStyled = []string{"-"}, []string{"-"}
		}

		r.serving = servingLabel(c)

		if o.Verbose && len(c.Attempts) > 0 {
			var tried []string
			for _, a := range c.Attempts {
				s := a.Upstream + " " + strings.Trim(a.Phase+"→"+a.Result, "→")
				if a.Won {
					s += " (won)"
				}
				tried = append(tried, s)
			}

			r.detail = "tried: " + strings.Join(tried, ", ")
		}

		rows = append(rows, r)
	}

	wNet, wChain, wUps := len("NETWORK"), len("CHAIN"), len("UPSTREAMS")
	for _, r := range rows {
		wNet = max(wNet, len(r.network))
		wChain = max(wChain, len(r.chain))
		for _, s := range r.ups {
			wUps = max(wUps, lipgloss.Width(s))
		}
	}

	b.blank()

	header := fmt.Sprintf("%-*s  %-*s  %-*s", wNet, "NETWORK", wChain, "CHAIN", wUps, "UPSTREAMS")
	if !o.NoProbe {
		header += "  SERVING NOW"
	}
	b.linef("%s%s", indent, ui.Paint("dim", strings.TrimRight(header, " ")))

	for _, r := range rows {
		for i, plain := range r.ups {
			netCol, chainCol := "", ""
			if i == 0 {
				netCol, chainCol = r.network, r.chain
			}

			line := fmt.Sprintf("%-*s  %-*s  %s%s", wNet, netCol, wChain, chainCol, r.upsStyled[i],
				strings.Repeat(" ", wUps-lipgloss.Width(plain)))
			if i == 0 && !o.NoProbe {
				line += "  " + r.serving
			}

			b.linef("%s%s", indent, strings.TrimRight(line, " "))
		}

		if r.detail != "" {
			b.linef("%s%s%s", indent, strings.Repeat(" ", wNet+wChain+4), ui.Paint("dim", r.detail))
		}
	}
}

func servingLabel(c network.ERPCChain) string {
	if c.Serving == "" {
		if c.ProbeError != "" {
			return ui.Paint("fail", ui.GlyphFail) + " " + ui.Paint("dim", shortError(c.ProbeError, 48))
		}

		return ui.Paint("dim", "-")
	}

	label := c.Serving
	for _, up := range c.Upstreams {
		if up.ID == c.Serving && up.Local {
			label = ui.Paint("ok", "local "+up.Deployment)
		}
	}

	if c.FromCache {
		label += ui.Paint("dim", " (cache)")
	}

	return label
}

func upstreamStateLabel(state string) string {
	switch state {
	case network.VerdictNotProbed:
		return "not probed"
	case network.VerdictELOffline:
		return "EL offline"
	case network.VerdictNotDeployed:
		return "not deployed"
	default:
		return state
	}
}

// verdictKind maps a verdict to a Paint kind.
func verdictKind(state string) string {
	switch state {
	case network.VerdictSynced:
		return "ok"
	case network.VerdictSyncing, network.VerdictWaiting:
		return "warn"
	case network.VerdictStopped, network.VerdictNotProbed, network.VerdictNotSupported:
		return "dim"
	default:
		return "fail"
	}
}

// erpcPodLine renders an eRPC pod as "pod Running · ready"; the pod name
// only shows under --verbose.
func erpcPodLine(p network.ERPCPod, verbose bool) string {
	out := "pod " + p.Phase
	if p.Ready {
		out += " · ready"
	} else {
		out += " · not ready"
		if p.Reason != "" {
			out += " (" + p.Reason + ")"
		}
	}

	if p.Restarts > 0 {
		out += fmt.Sprintf(" · %d restarts", p.Restarts)
	}

	if verbose {
		out += " (" + p.Name + ")"
	}

	return out
}

// nodeHeading is "ethereum/mainnet  ·  reth + lighthouse  ·  archive …".
func nodeHeading(n network.NodeStatus) string {
	parts := []string{n.Identifier()}
	if n.ExecutionClient != "" || n.ConsensusClient != "" {
		parts = append(parts, n.ExecutionClient+" + "+n.ConsensusClient)
	}

	if mode := modeLabel(n.Deployment); mode != "" {
		parts = append(parts, mode)
	}

	if n.Network != "" && !strings.HasSuffix(n.ID, n.Network) {
		parts = append(parts, n.Network)
	}

	return strings.Join(parts, "  ·  ")
}

func renderNode(b *statusBuilder, n network.NodeStatus, chains []network.ERPCChain, o renderOptions) {
	title := "Local node  " + nodeHeading(n)
	if n.Type != network.TypeEthereum {
		title = "Local deployment  " + nodeHeading(n)
	}

	b.linef("%s", ui.Heading(title))
	b.linef("%s%s", indent, nodeHeadline(n, chains))

	switch n.Verdict.State {
	case network.VerdictNotProbed, network.VerdictNotSupported:
		return
	case network.VerdictStopped:
		renderNodeFooter(b, n, o)
		return
	}

	if n.PodsError != "" {
		b.linef("%s%s Could not list pods: %s", indent, ui.Paint("fail", ui.GlyphFail), shortError(n.PodsError, 120))
	}

	b.blank()
	renderExecution(b, n, o)
	renderConsensus(b, n, o)

	var other []network.PodProblem
	for _, p := range n.Problems {
		if p.Component != "execution" && p.Component != "beacon" {
			other = append(other, p)
		}
	}

	if len(other) > 0 {
		b.linef("%s%s", indent, ui.Paint("bold", "Other pods"))
		renderProblems(b, other)
	}

	renderNodeFooter(b, n, o)
}

func renderNodeFooter(b *statusBuilder, n network.NodeStatus, o renderOptions) {
	b.blank()

	if s := n.Storage; s != nil {
		prefix := ""
		if s.Partial {
			prefix = "≥"
		}

		line := fmt.Sprintf("exec %s%s · cons %s%s", prefix, network.FormatBytes(s.ExecutionBytes), prefix, network.FormatBytes(s.ConsensusBytes))
		if s.FreeBytes != nil {
			line += fmt.Sprintf(" · %s free", network.FormatBytes(*s.FreeBytes))
		}

		if o.Verbose {
			line += ui.Paint("dim", "  ("+s.Path+")")
		}

		b.linef("%s%s  %s", indent, ui.Paint("dim", fmt.Sprintf("%-9s", "Data")), line)
	}

	if o.Verbose && len(n.Pods) > 0 {
		var pods []string
		for _, p := range n.Pods {
			s := p.Name + " " + p.Phase
			if p.Ready {
				s += " ready"
			} else {
				s += " not ready"
			}

			if p.Restarts > 0 {
				s += fmt.Sprintf(" (%d restarts)", p.Restarts)
			}

			if p.MemoryRequest != "" {
				s += " req " + p.MemoryRequest
			}

			pods = append(pods, s)
		}

		b.linef("%s%s  %s", indent, ui.Paint("dim", fmt.Sprintf("%-9s", "Pods")), strings.Join(pods, " · "))
	}

	if n.GrafanaURL != "" {
		b.linef("%s%s  %s", indent, ui.Paint("dim", fmt.Sprintf("%-9s", "Grafana")), n.GrafanaURL)
	}
}

// nodeHeadline is the verdict as one glyph-led sentence.
func nodeHeadline(n network.NodeStatus, chains []network.ERPCChain) string {
	v := n.Verdict
	ident := n.Identifier()

	serving := ""
	registered := false
	if n.ERPC != nil {
		serving, registered = n.ERPC.Serving, n.ERPC.Registered
	}

	switch v.State {
	case network.VerdictSynced:
		s := ui.Paint("ok", ui.GlyphOK) + " Synced"
		if el := n.Execution; el != nil && el.HeadBlock > 0 {
			s += " — head block " + commas(el.HeadBlock)
		}

		switch {
		case n.ERPC == nil:
		case !registered:
			s += "; " + ui.Paint("warn", "not registered with eRPC") + " (obol network sync " + ident + ")"
		case serving == n.ERPC.Upstream:
			s += "; serving " + networkRoute(n, chains)
		case serving != "":
			s += "; eRPC currently answers from " + serving
		}

		return s
	case network.VerdictSyncing:
		s := ui.Paint("warn", ui.GlyphBusy) + " Syncing"
		if v.ProgressPct != nil {
			s += fmt.Sprintf(" %.1f%%", *v.ProgressPct)
		}

		s += " — not serving RPC yet"
		if serving != "" && (n.ERPC == nil || serving != n.ERPC.Upstream) {
			s += " (eRPC falls back to " + serving + ")"
		}

		return s
	case network.VerdictWaiting:
		return ui.Paint("warn", ui.GlyphBusy) + " Waiting — " + v.Summary
	case network.VerdictStopped:
		data := "data kept"
		if n.Storage != nil && n.Storage.TotalBytes() > 0 {
			data = "data kept, " + network.FormatBytes(n.Storage.TotalBytes())
		}

		return ui.Paint("dim", ui.GlyphStopped) + " Stopped (" + data + ") — start with: obol network start " + ident
	case network.VerdictNotDeployed:
		return ui.Paint("fail", ui.GlyphFail) + " Not deployed — no node pods (obol network sync " + ident + ")"
	case network.VerdictNotProbed, network.VerdictNotSupported:
		return ui.Paint("dim", ui.GlyphPending+" "+capitalize(v.Summary))
	default:
		return ui.Paint("fail", ui.GlyphFail) + " " + capitalize(v.Summary)
	}
}

func networkRoute(n network.NodeStatus, chains []network.ERPCChain) string {
	for _, c := range chains {
		for _, up := range c.Upstreams {
			if n.ERPC != nil && up.ID == n.ERPC.Upstream {
				return "/rpc/" + c.Network
			}
		}
	}

	return "/rpc/" + n.Network
}

func componentProblems(n network.NodeStatus, component string) []network.PodProblem {
	var out []network.PodProblem
	for _, p := range n.Problems {
		if p.Component == component {
			out = append(out, p)
		}
	}

	return out
}

func hasSevere(problems []network.PodProblem) bool {
	return slices.ContainsFunc(problems, network.PodProblem.Severe)
}

func renderExecution(b *statusBuilder, n network.NodeStatus, o renderOptions) {
	el := n.Execution
	if el == nil {
		return
	}

	head := []string{clientLabel(n.ExecutionClient, el.ClientVersion, o.Verbose)}
	if el.Peers != nil {
		head = append(head, peersLabel(*el.Peers))
	}

	b.linef("%s%s  %s", indent, ui.Paint("bold", fmt.Sprintf("%-9s", "Execution")), strings.Join(head, "  ·  "))

	problems := componentProblems(n, "execution")
	renderProblems(b, problems)

	if !el.Reachable {
		if !hasSevere(problems) {
			b.linef("%s%s JSON-RPC %s", stageIndent, ui.Paint("fail", ui.GlyphFail), shortError(el.Error, 100))
		}

		return
	}

	switch {
	case len(el.Stages) > 0:
		renderStages(b, el.Stages, o)
	case el.Syncing && el.HighestBlock > 0:
		renderStages(b, []network.SyncStage{{
			Name: "Syncing", State: network.StageCurrent, Block: el.CurrentBlock, Target: el.HighestBlock, Unit: network.UnitBlocks,
		}}, o)
	case el.Syncing:
		b.linef("%s%s Syncing (no progress reported yet)", stageIndent, ui.Paint("accent", ui.GlyphCurrent))
	default:
		b.linef("%s%s At head  block %s", stageIndent, ui.Paint("ok", ui.GlyphOK), commas(el.HeadBlock))
	}

	if o.Verbose {
		b.linef("%s%s", stageIndent, ui.Paint("dim", fmt.Sprintf("syncing=%t head=%d current=%d highest=%d",
			el.Syncing, el.HeadBlock, el.CurrentBlock, el.HighestBlock)))
	}
}

func renderConsensus(b *statusBuilder, n network.NodeStatus, o renderOptions) {
	cl := n.Consensus
	if cl == nil {
		return
	}

	head := []string{clientLabel(n.ConsensusClient, cl.Version, o.Verbose)}
	if cl.Peers != nil {
		head = append(head, peersLabel(*cl.Peers))
	}

	b.linef("%s%s  %s", indent, ui.Paint("bold", fmt.Sprintf("%-9s", "Consensus")), strings.Join(head, "  ·  "))

	problems := componentProblems(n, "beacon")
	renderProblems(b, problems)

	if !cl.Reachable {
		if !hasSevere(problems) {
			b.linef("%s%s Beacon API %s", stageIndent, ui.Paint("fail", ui.GlyphFail), shortError(cl.Error, 100))
		}

		return
	}

	if cl.ELOffline {
		b.linef("%s%s Can't reach the execution client over the engine API (el_offline)", stageIndent, ui.Paint("fail", ui.GlyphFail))
	}

	renderStages(b, cl.Stages, o)

	if o.Verbose {
		raw := fmt.Sprintf("is_syncing=%t is_optimistic=%t el_offline=%t head_slot=%d sync_distance=%d",
			cl.IsSyncing, cl.IsOptimistic, cl.ELOffline, cl.HeadSlot, cl.SyncDistance)
		if cl.SyncState != "" {
			raw += " lighthouse=" + cl.SyncState
		}

		b.linef("%s%s", stageIndent, ui.Paint("dim", raw))
	}
}

func renderProblems(b *statusBuilder, problems []network.PodProblem) {
	for _, p := range problems {
		glyph := ui.Paint("fail", ui.GlyphFail)
		if !p.Severe() {
			glyph = ui.Paint("warn", ui.GlyphBusy)
		}

		b.linef("%s%s %s", stageIndent, glyph, p.Message)

		if p.Hint != "" {
			b.linef("%s  %s", stageIndent, ui.Paint("dim", "Hint: "+p.Hint))
		}
	}
}

// renderStages prints done and current stages with progress; pending ones
// are collapsed into one line unless verbose.
func renderStages(b *statusBuilder, stages []network.SyncStage, o renderOptions) {
	wName, wNum := 0, 0
	for _, s := range stages {
		wName = max(wName, len(s.Name))
		if s.State != network.StagePending || o.Verbose {
			wNum = max(wNum, len(commas(s.Block)), len(commas(s.Target)))
		}
	}

	var pending []string

	for _, s := range stages {
		if s.State == network.StagePending && !o.Verbose {
			pending = append(pending, s.Name)
			continue
		}

		// A done stage still at 0 was skipped (prune config, unused era import).
		if s.State == network.StageDone && s.Block == 0 && s.Target > 0 && !o.Verbose {
			continue
		}

		glyph := map[string]string{
			network.StageDone:    ui.Paint("ok", ui.GlyphOK),
			network.StageCurrent: ui.Paint("accent", ui.GlyphCurrent),
			network.StageStalled: ui.Paint("fail", ui.GlyphFail),
			network.StagePending: ui.Paint("dim", ui.GlyphPending),
		}[s.State]

		line := fmt.Sprintf("%s %-*s", glyph, wName, s.Name)

		switch {
		case s.Target > 0 && (s.State != network.StageDone || s.Block > 0):
			line += fmt.Sprintf("  %*s / %-*s", wNum, commas(s.Block), wNum, commas(s.Target))
		case s.Block > 0:
			unit := "block"
			if s.Unit == network.UnitSlots {
				unit = "slot"
			}

			line += "  " + unit + " " + commas(s.Block)
		}

		if s.State == network.StageCurrent {
			if pct, ok := s.Pct(); ok {
				line += fmt.Sprintf("  %5.1f%%", pct)
			}

			if s.RatePerSec != nil {
				line += "  ·  " + rateLabel(*s.RatePerSec, s.Unit)
				if s.ETASeconds != nil {
					line += "  ·  ETA " + etaLabel(*s.ETASeconds)
				} else if *s.RatePerSec <= 0 {
					line += "  ·  " + ui.Paint("warn", "stalled?")
				}
			} else if o.Watch {
				line += "  ·  " + ui.Paint("dim", "measuring rate…")
			}
		}

		b.linef("%s%s", stageIndent, strings.TrimRight(line, " "))
	}

	if len(pending) > 0 {
		shown := pending
		more := ""
		if len(shown) > 4 {
			shown, more = shown[:4], fmt.Sprintf(", +%d more", len(pending)-4)
		}

		b.linef("%s%s", stageIndent, ui.Paint("dim", ui.GlyphPending+" pending: "+strings.Join(shown, ", ")+more))
	}
}

func rateLabel(rate float64, unit string) string {
	suffix := "blk/s"
	if unit == network.UnitSlots {
		suffix = "slots/s"
	}

	if rate < 10 {
		return fmt.Sprintf("%.1f %s", rate, suffix)
	}

	return commas(uint64(rate+0.5)) + " " + suffix
}

func etaLabel(sec int64) string {
	d := time.Duration(sec) * time.Second

	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours()/24), int(d.Hours())%24)
	}
}

func peersLabel(n uint64) string {
	s := fmt.Sprintf("%d peers", n)
	if n == 1 {
		s = "1 peer"
	}

	if n == 0 {
		return ui.Paint("warn", s)
	}

	return s
}

// clientLabel is "reth v2.7.0" (the full version string under --verbose).
func clientLabel(client, version string, verbose bool) string {
	if version == "" {
		return client
	}

	if verbose {
		return version
	}

	return shortClientVersion(version)
}

// shortClientVersion turns "reth/v2.7.0-3d592ec/x86_64-unknown-linux-gnu"
// into "reth v2.7.0": the client name and version without commit or
// platform.
func shortClientVersion(v string) string {
	parts := strings.SplitN(v, "/", 3)
	if len(parts) < 2 {
		return v
	}

	name := strings.ToLower(parts[0])
	ver, _, _ := strings.Cut(parts[1], "+")

	// Drop a trailing "-<commit>" (7+ hex chars).
	if i := strings.LastIndex(ver, "-"); i > 0 && isHexCommit(ver[i+1:]) {
		ver = ver[:i]
	}

	return name + " " + ver
}

func isHexCommit(s string) bool {
	if len(s) < 7 {
		return false
	}

	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}

	return true
}

// shortError keeps errors to one readable line.
func shortError(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "…"
	}

	return s
}

// commas renders n with thousands separators.
func commas(n uint64) string {
	s := strconv.FormatUint(n, 10)

	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}

		b.WriteRune(c)
	}

	return b.String()
}
