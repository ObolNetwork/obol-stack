package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

func tunnelCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "tunnel",
		Usage: "Manage Cloudflare tunnel for public access",
		Commands: []*cli.Command{
			{
				Name:  "status",
				Usage: "Show tunnel status and public URL",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "no-probe", Usage: "Skip connector and public reachability probes (offline/fast)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return tunnel.Status(cfg, getUI(cmd), tunnel.StatusOptions{NoProbe: cmd.Bool("no-probe")})
				},
			},
			{
				Name:      "setup",
				Usage:     "Create a permanent public URL with a Cloudflare tunnel",
				ArgsUsage: "[<connector-token>]",
				Description: "A tunnel exposes your stack to the public internet so buyers can discover and\n" +
					"pay for the services you sell. You don't need it for local use — set one up\n" +
					"once you're ready to sell, to get a permanent URL.\n\n" +
					"By default Obol wires a dashboard-managed tunnel from a Cloudflare connector\n" +
					"token (least privilege, no API key, no local install). Create the tunnel in the\n" +
					"Cloudflare dashboard, route its Public Hostname to\n" +
					"http://traefik.traefik.svc.cluster.local:80, then paste the token here — you can\n" +
					"paste the whole 'cloudflared tunnel run --token …' line and Obol extracts it.\n\n" +
					"No domain yet? Buy or transfer one in the Cloudflare dashboard first — it must\n" +
					"be a zone in your account.",
				Flags: tunnelSetupFlags(),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					opts, err := setupOptionsFromCommand(cmd)
					if err != nil {
						return err
					}
					result, err := tunnel.Setup(cfg, u, opts)
					if err != nil {
						return err
					}
					if u.IsJSON() {
						return u.JSON(result)
					}
					u.Blank()
					u.Successf("Tunnel ready: %s", result.URL)
					u.OpenURL("Storefront", dashboardURL(cfg, stack.LinkStorefront, "", ""), false)
					return nil
				},
			},
			tunnelHostnameCommand(cfg),
			{
				Name:  "restart",
				Usage: "Restart the tunnel connector (quick tunnels get a new URL)",
				Flags: []cli.Flag{tunnelTransportProtocolFlag()},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return tunnel.Restart(cfg, getUI(cmd), tunnel.RestartOptions{TransportProtocol: cmd.String("transport-protocol")})
				},
			},
			{
				Name:  "stop",
				Usage: "Pause the tunnel (scale cloudflared to 0; config preserved — use 'obol tunnel delete' to tear down)",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return tunnel.Stop(cfg, getUI(cmd))
				},
			},
			{
				Name:    "delete",
				Aliases: []string{"teardown"},
				Usage:   "Tear down the persistent tunnel (deletes the Cloudflare tunnel + cluster state; reverts to a quick tunnel)",
				Description: "Permanently removes the persistent tunnel created by 'obol tunnel setup':\n" +
					"deletes the Cloudflare tunnel (local-managed, cert-scoped), removes the\n" +
					"in-cluster connector resources and storefront, clears local state, and reverts\n" +
					"the connector to a default quick tunnel. Unlike 'obol tunnel stop' (which only\n" +
					"pauses the connector), this cannot be undone — re-run 'obol tunnel setup' to\n" +
					"create a new one.\n\n" +
					"DNS records and dashboard-managed tunnels live in Cloudflare (Obol holds no\n" +
					"account-wide API token), so those steps are printed for you to finish.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "force", Aliases: []string{"f"}, Usage: "Skip the confirmation prompt"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					result, err := tunnel.Delete(cfg, u, tunnel.DeleteOptions{Force: cmd.Bool("force")})
					if err != nil {
						return err
					}
					if u.IsJSON() {
						return u.JSON(result)
					}
					return nil
				},
			},
			{
				Name:  "logs",
				Usage: "View cloudflared logs",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, Usage: "Follow log output"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return tunnel.Logs(cfg, cmd.Bool("follow"))
				},
			},
		},
	}
}

// tunnelHostnameCommand manages multiple public hostnames on one tunnel:
// "deploy one domain, then another". Each hostname serves the same x402-gated
// service surface (offer HTTPRoutes are host-agnostic), so adding a second
// hostname never takes down the first.
func tunnelHostnameCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:    "hostname",
		Aliases: []string{"hostnames", "domains"},
		Usage:   "Add, list, and remove additional public hostnames on the tunnel",
		Description: "A permanent tunnel can serve more than one public hostname at once. Set up the\n" +
			"first with 'obol tunnel setup', then add a second (and Nth) here. Every hostname\n" +
			"serves the same payment-gated services — adding one never disrupts the others.\n\n" +
			"Local-managed tunnels (browser cert) are fully automated: Obol routes the DNS\n" +
			"record, re-renders the connector ingress over all hostnames, and reloads it.\n" +
			"Dashboard-managed (connector token) tunnels keep their ingress in Cloudflare, so\n" +
			"Obol tracks the hostname, updates the storefront route, and prints the one\n" +
			"dashboard step to finish.",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List every public hostname served by the tunnel",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					result, err := tunnel.ListHostnames(cfg)
					if err != nil {
						return err
					}
					if u.IsJSON() {
						return u.JSON(result)
					}
					printHostnameList(u, result)
					return nil
				},
			},
			{
				Name:      "add",
				Usage:     "Add another public hostname to the tunnel",
				ArgsUsage: "<hostname>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "hostname", Aliases: []string{"H"}, Usage: "Public hostname to add (or pass as a positional argument)"},
					&cli.StringFlag{Name: "offer", Usage: "Bind the hostname to a ServiceOffer as its dedicated origin (<namespace>/<name>). " +
						"Patches the offer's spec.hostname; the controller renders the per-offer routes + discovery bundle, and the shared storefront skips this hostname."},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					hostname := strings.TrimSpace(cmd.String("hostname"))
					if hostname == "" {
						hostname = strings.TrimSpace(cmd.Args().First())
					}
					// Bind BEFORE the tunnel-side render: AddHostname
					// re-renders the storefront catch-all, which must
					// already see the offer claim so it skips this host.
					if offerRef := strings.TrimSpace(cmd.String("offer")); offerRef != "" {
						if err := bindHostnameToOffer(cfg, u, hostname, offerRef); err != nil {
							return err
						}
					}
					result, err := tunnel.AddHostname(cfg, u, tunnel.AddHostnameOptions{
						Hostname: hostname,
					})
					if err != nil {
						return err
					}
					if u.IsJSON() {
						return u.JSON(result)
					}
					return nil
				},
			},
			{
				Name:      "remove",
				Aliases:   []string{"rm", "delete"},
				Usage:     "Remove a public hostname from the tunnel (others keep serving)",
				ArgsUsage: "<hostname>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "hostname", Aliases: []string{"H"}, Usage: "Public hostname to remove (or pass as a positional argument)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					hostname := strings.TrimSpace(cmd.String("hostname"))
					if hostname == "" {
						hostname = strings.TrimSpace(cmd.Args().First())
					}
					result, err := tunnel.RemoveHostname(cfg, u, tunnel.RemoveHostnameOptions{
						Hostname: hostname,
					})
					if err != nil {
						return err
					}
					if u.IsJSON() {
						return u.JSON(result)
					}
					return nil
				},
			},
		},
	}
}

// bindHostnameToOffer patches spec.hostname on the referenced ServiceOffer.
// The serviceoffer-controller then renders the dedicated-origin HTTPRoute
// and discovery bundle; the tunnel side only has to route DNS and keep the
// storefront catch-all off the host.
func bindHostnameToOffer(cfg *config.Config, u *ui.UI, hostname, offerRef string) error {
	ns, name, ok := strings.Cut(offerRef, "/")
	if !ok || ns == "" || name == "" {
		return fmt.Errorf("--offer must be <namespace>/<name>, got %q", offerRef)
	}
	host := strings.ToLower(strings.TrimSpace(hostname))
	if host == "" {
		return fmt.Errorf("--offer requires a hostname to bind")
	}
	bin, kubeconfig := kubectl.Paths(cfg)
	patch := fmt.Sprintf(`{"spec":{"hostname":%q}}`, host)
	if _, err := kubectl.Output(bin, kubeconfig, "patch", "serviceoffers.obol.org", name, "-n", ns, "--type=merge", "-p", patch); err != nil {
		return fmt.Errorf("bind hostname to offer %s/%s: %w", ns, name, err)
	}
	u.Successf("Bound %s to offer %s/%s (spec.hostname)", host, ns, name)
	return nil
}

func printHostnameList(u *ui.UI, result *tunnel.HostnameListResult) {
	u.Blank()
	u.Bold("Tunnel Hostnames")
	if len(result.Hostnames) == 0 {
		u.Print("No permanent hostnames configured.")
		u.Dim("Create one with: obol tunnel setup")
		return
	}
	for _, h := range result.Hostnames {
		label := h.URL
		if h.Primary {
			label += "  (primary)"
		}
		u.Print("- " + label)
	}
	u.Blank()
	u.Dim("Add another: obol tunnel hostname add <subdomain>.<domain>")
	u.Dim("Remove one:  obol tunnel hostname remove <hostname>")
}

func tunnelTransportProtocolFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "transport-protocol",
		Usage: "Cloudflared edge transport: auto, quic, or http2 (defaults to auto)",
	}
}

func tunnelSetupFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "hostname", Aliases: []string{"H"}, Usage: "Public hostname to route (e.g. stack.example.com)"},
		&cli.StringFlag{Name: "token", Aliases: []string{"t"}, Usage: "Cloudflare tunnel connector token (or pass it as a positional argument)"},
		tunnelTransportProtocolFlag(),
		&cli.StringFlag{Name: "from-json", Usage: "Read setup options from JSON file (or - for stdin)"},
	}
}

func setupOptionsFromCommand(cmd *cli.Command) (tunnel.SetupOptions, error) {
	if jsonPath := cmd.String("from-json"); jsonPath != "" {
		var opts tunnel.SetupOptions
		data, err := readJSONInput(jsonPath)
		if err != nil {
			return opts, err
		}
		if err := json.Unmarshal(data, &opts); err != nil {
			return opts, fmt.Errorf("parse setup JSON: %w", err)
		}
		return opts, nil
	}

	token := strings.TrimSpace(cmd.String("token"))
	if token == "" {
		token = strings.TrimSpace(cmd.Args().First())
	}

	return tunnel.SetupOptions{
		Hostname:          cmd.String("hostname"),
		ConnectorToken:    token,
		TransportProtocol: cmd.String("transport-protocol"),
	}, nil
}
