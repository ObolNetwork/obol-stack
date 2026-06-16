package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
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
					"No domain yet? Register one from the CLI with 'obol domain', or buy/transfer one\n" +
					"in the Cloudflare dashboard first — either way it must be a zone in your account.\n\n" +
					"Advanced: '--management local' uses a browser login on this machine instead\n" +
					"(needs cloudflared installed); 'obol tunnel login' is the same flow directly.",
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
					return nil
				},
			},
			{
				Name:   "login",
				Hidden: true,
				Usage:  "Advanced: create a locally-managed tunnel via browser login (no token)",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "hostname",
						Aliases:  []string{"H"},
						Usage:    "Public hostname to route (e.g. stack.example.com)",
						Required: true,
					},
					tunnelTransportProtocolFlag(),
					&cli.BoolFlag{
						Name:  "overwrite-dns",
						Usage: "Replace any existing A/AAAA/CNAME at the hostname (forwards --overwrite-dns to cloudflared)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return tunnel.Login(cfg, getUI(cmd), tunnel.LoginOptions{
						Hostname:          cmd.String("hostname"),
						TransportProtocol: cmd.String("transport-protocol"),
						OverwriteDNS:      cmd.Bool("overwrite-dns"),
					})
				},
			},
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
				Usage: "Stop the tunnel (scale cloudflared to 0 replicas)",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return tunnel.Stop(cfg, getUI(cmd))
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

func domainCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "domain",
		Usage: "Search, check, and register Cloudflare Registrar domains (optional)",
		Description: "Buying a domain through Obol is entirely optional — it's a convenience wrapper\n" +
			"around Cloudflare Registrar so you can get a domain without leaving the CLI.\n" +
			"If you'd rather, buy or transfer a domain in the Cloudflare dashboard before\n" +
			"setting up a tunnel; anything that lands as a zone in your account works.\n\n" +
			"These commands need a scoped Cloudflare API token (with the Account → Domain\n" +
			"permission) — separate from the tunnel connector token — and registering a\n" +
			"domain is billable, so your Cloudflare account needs a saved payment method.\n\n" +
			"Once you own a domain, give your stack a permanent URL with 'obol tunnel setup'.",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "List domains already registered in your Cloudflare account",
				Flags: domainAuthFlags(),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					opts, err := domainListOptionsFromCommand(cmd, u)
					if err != nil {
						return err
					}
					result, err := tunnel.ListDomains(opts)
					if err != nil {
						return err
					}
					if u.IsJSON() {
						return u.JSON(result)
					}
					printDomainList(u, result)
					return nil
				},
			},
			{
				Name:  "search",
				Usage: "Search for available Cloudflare Registrar domains",
				Flags: append([]cli.Flag{
					&cli.StringFlag{Name: "query", Aliases: []string{"q"}, Usage: "Keyword, phrase, or domain to search for"},
					&cli.StringSliceFlag{Name: "extensions", Usage: "Optional extension filter(s), e.g. --extensions com --extensions dev"},
					&cli.IntFlag{Name: "limit", Usage: "Maximum number of suggestions to return", Value: 10},
				}, domainAuthFlags()...),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					opts, err := domainSearchOptionsFromCommand(cmd, u)
					if err != nil {
						return err
					}
					result, err := tunnel.SearchDomains(opts)
					if err != nil {
						return err
					}
					if u.IsJSON() {
						return u.JSON(result)
					}
					printDomainSuggestions(u, result)
					return nil
				},
			},
			{
				Name:      "check",
				Usage:     "Check authoritative availability for one or more domains",
				ArgsUsage: "<domain> [<domain> ...]",
				Flags:     domainAuthFlags(),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					opts, err := domainCheckOptionsFromCommand(cmd, u)
					if err != nil {
						return err
					}
					result, err := tunnel.CheckDomains(opts)
					if err != nil {
						return err
					}
					if u.IsJSON() {
						return u.JSON(result)
					}
					printDomainChecks(u, result)
					return nil
				},
			},
			{
				Name:      "register",
				Usage:     "Register a domain through Cloudflare Registrar (billable)",
				ArgsUsage: "<domain>",
				Flags: append([]cli.Flag{
					&cli.IntFlag{Name: "years", Usage: "Registration term in years (default 1 or registry minimum)", Value: 1},
					&cli.BoolFlag{Name: "auto-renew", Usage: "Enable automatic renewal"},
					&cli.StringFlag{Name: "privacy-mode", Usage: "WHOIS privacy mode", Value: "redaction"},
					&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "Confirm the billable registration without prompting"},
					&cli.BoolFlag{Name: "respond-async", Hidden: true, Usage: "Request an immediate async workflow response from Cloudflare"},
				}, domainAuthFlags()...),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					opts, err := domainRegisterOptionsFromCommand(cmd, u)
					if err != nil {
						return err
					}
					result, err := tunnel.RegisterDomain(u, opts)
					if err != nil {
						return err
					}
					if u.IsJSON() {
						return u.JSON(result)
					}
					printDomainRegistration(u, result)
					return nil
				},
			},
		},
	}
}

// domainAuthFlags are the shared Cloudflare credential flags for `obol domain`.
// Note: --api-token deliberately has no -t alias, to avoid colliding with
// `obol tunnel setup -t` (which takes a tunnel connector token, a different
// credential). The token here is a scoped Cloudflare API token.
func domainAuthFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "account-id", Aliases: []string{"a"}, Usage: "Cloudflare account ID", Sources: cli.EnvVars("CLOUDFLARE_ACCOUNT_ID")},
		&cli.StringFlag{Name: "api-token", Usage: "Cloudflare API token (Account → Domain permission)", Sources: cli.EnvVars("CLOUDFLARE_API_TOKEN")},
		&cli.StringFlag{Name: "from-json", Usage: "Read options from JSON file (or - for stdin)"},
	}
}

// resolveDomainAPIToken returns the Cloudflare API token from the flag/env or,
// in an interactive session, walks the user through creating a scoped token and
// prompts for it. This mirrors the tunnel connector-token flow.
func resolveDomainAPIToken(u *ui.UI, supplied string) (string, error) {
	if token := strings.TrimSpace(supplied); token != "" {
		return token, nil
	}

	if !u.IsTTY() || u.IsJSON() {
		return "", errors.New("a Cloudflare API token is required: pass --api-token or set CLOUDFLARE_API_TOKEN.\n" +
			"Create one with the Account → Domain permission at https://dash.cloudflare.com/profile/api-tokens")
	}

	u.Blank()
	u.Bold("Cloudflare API token needed")
	u.Print("Managing Cloudflare Registrar domains from the CLI needs a scoped API token.")
	u.Dim("This is a different credential from the tunnel connector token.")
	u.Print("  1. Open https://dash.cloudflare.com/profile/api-tokens → Create Token")
	u.Print("  2. Grant the Account → Domain permission (and select your account).")
	u.Print("  3. Create the token and copy it.")
	u.Blank()
	token, err := u.SecretInput("Paste your Cloudflare API token")
	if err != nil {
		return "", err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("no Cloudflare API token provided")
	}
	return token, nil
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
		&cli.StringFlag{Name: "management", Usage: "Tunnel management: connector (default) or local (browser fallback)", Value: "connector"},
		tunnelTransportProtocolFlag(),
		&cli.BoolFlag{Name: "overwrite-dns", Usage: "Local-managed only: replace any existing A/AAAA/CNAME at the hostname"},
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
		Management:        cmd.String("management"),
		ConnectorToken:    token,
		TransportProtocol: cmd.String("transport-protocol"),
		OverwriteDNS:      cmd.Bool("overwrite-dns"),
	}, nil
}

func domainListOptionsFromCommand(cmd *cli.Command, u *ui.UI) (tunnel.DomainListOptions, error) {
	if jsonPath := cmd.String("from-json"); jsonPath != "" {
		var opts tunnel.DomainListOptions
		data, err := readJSONInput(jsonPath)
		if err != nil {
			return opts, err
		}
		if err := json.Unmarshal(data, &opts); err != nil {
			return opts, fmt.Errorf("parse domain list JSON: %w", err)
		}
		return opts, nil
	}

	token, err := resolveDomainAPIToken(u, cmd.String("api-token"))
	if err != nil {
		return tunnel.DomainListOptions{}, err
	}

	return tunnel.DomainListOptions{
		AccountID: cmd.String("account-id"),
		APIToken:  token,
	}, nil
}

func domainSearchOptionsFromCommand(cmd *cli.Command, u *ui.UI) (tunnel.DomainSearchOptions, error) {
	if jsonPath := cmd.String("from-json"); jsonPath != "" {
		var opts tunnel.DomainSearchOptions
		data, err := readJSONInput(jsonPath)
		if err != nil {
			return opts, err
		}
		if err := json.Unmarshal(data, &opts); err != nil {
			return opts, fmt.Errorf("parse domain search JSON: %w", err)
		}
		return opts, nil
	}

	query := cmd.String("query")
	if strings.TrimSpace(query) == "" {
		input, err := u.Input("Search query", "")
		if err != nil {
			return tunnel.DomainSearchOptions{}, err
		}
		query = input
	}

	token, err := resolveDomainAPIToken(u, cmd.String("api-token"))
	if err != nil {
		return tunnel.DomainSearchOptions{}, err
	}

	return tunnel.DomainSearchOptions{
		Query:      query,
		Extensions: cmd.StringSlice("extensions"),
		Limit:      cmd.Int("limit"),
		AccountID:  cmd.String("account-id"),
		APIToken:   token,
	}, nil
}

func domainCheckOptionsFromCommand(cmd *cli.Command, u *ui.UI) (tunnel.DomainCheckOptions, error) {
	if jsonPath := cmd.String("from-json"); jsonPath != "" {
		var opts tunnel.DomainCheckOptions
		data, err := readJSONInput(jsonPath)
		if err != nil {
			return opts, err
		}
		if err := json.Unmarshal(data, &opts); err != nil {
			return opts, fmt.Errorf("parse domain check JSON: %w", err)
		}
		return opts, nil
	}

	token, err := resolveDomainAPIToken(u, cmd.String("api-token"))
	if err != nil {
		return tunnel.DomainCheckOptions{}, err
	}

	return tunnel.DomainCheckOptions{
		Domains:   cmd.Args().Slice(),
		AccountID: cmd.String("account-id"),
		APIToken:  token,
	}, nil
}

func domainRegisterOptionsFromCommand(cmd *cli.Command, u *ui.UI) (tunnel.DomainRegisterOptions, error) {
	if jsonPath := cmd.String("from-json"); jsonPath != "" {
		var opts tunnel.DomainRegisterOptions
		data, err := readJSONInput(jsonPath)
		if err != nil {
			return opts, err
		}
		if err := json.Unmarshal(data, &opts); err != nil {
			return opts, fmt.Errorf("parse domain registration JSON: %w", err)
		}
		return opts, nil
	}

	token, err := resolveDomainAPIToken(u, cmd.String("api-token"))
	if err != nil {
		return tunnel.DomainRegisterOptions{}, err
	}

	return tunnel.DomainRegisterOptions{
		DomainName:    cmd.Args().First(),
		Years:         cmd.Int("years"),
		AutoRenew:     cmd.Bool("auto-renew"),
		PrivacyMode:   cmd.String("privacy-mode"),
		ConfirmCharge: cmd.Bool("yes"),
		RespondAsync:  cmd.Bool("respond-async"),
		AccountID:     cmd.String("account-id"),
		APIToken:      token,
	}, nil
}

func printDomainList(u *ui.UI, result *tunnel.DomainListResult) {
	u.Blank()
	u.Bold("Registered Domains")
	if len(result.Domains) == 0 {
		u.Print("No domains registered in this Cloudflare account.")
		u.Dim("Find one with: obol domain search <keyword>")
		return
	}
	for _, domain := range result.Domains {
		u.Print("- " + domain.Name)
		if domain.ExpiresAt != "" {
			u.Detail("  Expires", domain.ExpiresAt)
		}
		renew := "off"
		if domain.AutoRenew {
			renew = "on"
		}
		u.Detail("  Auto-renew", renew)
	}
	u.Blank()
	u.Dim("Give your stack a permanent URL on one of these: obol tunnel setup --hostname <subdomain>.<domain>")
}

func printDomainSuggestions(u *ui.UI, result *tunnel.DomainSearchResult) {
	u.Blank()
	u.Bold("Domain Suggestions")
	registrable := false
	for _, domain := range result.Domains {
		summary := "not registrable"
		if domain.Registrable {
			summary = tunnelSummaryPrice(domain)
			registrable = true
		}
		if domain.Reason != "" {
			summary = summary + " — " + domain.Reason
		}
		u.Print("- " + domain.Name)
		u.Detail("  Status", summary)
	}
	if registrable {
		u.Blank()
		u.Dim("Register one with: obol domain register <name>")
	}
}

func printDomainChecks(u *ui.UI, result *tunnel.DomainCheckResult) {
	u.Blank()
	u.Bold("Domain Availability")
	registrable := false
	for _, domain := range result.Domains {
		status := "not registrable"
		if domain.Registrable {
			status = tunnelSummaryPrice(domain)
			registrable = true
		}
		if domain.Reason != "" {
			status = status + " — " + domain.Reason
		}
		u.Print("- " + domain.Name)
		u.Detail("  Status", status)
	}
	if registrable {
		u.Blank()
		u.Dim("Register one with: obol domain register <name>")
	}
}

func printDomainRegistration(u *ui.UI, result *tunnel.DomainRegisterResult) {
	u.Blank()
	u.Successf("Domain registration submitted for %s", result.Availability.Name)
	u.Detail("Price", tunnelSummaryPrice(result.Availability))
	if result.Workflow != nil {
		u.Detail("Workflow State", result.Workflow.State)
		if result.Workflow.Links.Self != "" {
			u.Detail("Workflow URL", result.Workflow.Links.Self)
		}
		if result.Workflow.Links.Resource != "" {
			u.Detail("Domain Resource", result.Workflow.Links.Resource)
		}
	}
	u.Blank()
	u.Bold("Next: put your new domain to work")
	u.Print("Give your stack a permanent public URL on it with a Cloudflare tunnel:")
	u.Printf("  obol tunnel setup --hostname <subdomain>.%s", result.Availability.Name)
	u.Dim("A Registrar domain is automatically a zone in your account — all the tunnel needs.")
}

func tunnelSummaryPrice(domain tunnel.CloudflareRegistrarDomainAlias) string {
	if domain.Pricing == nil || domain.Pricing.RegistrationCost == "" {
		return "registrable"
	}
	if domain.Pricing.Currency == "" {
		return domain.Pricing.RegistrationCost + "/year"
	}
	return domain.Pricing.Currency + " " + domain.Pricing.RegistrationCost + "/year"
}
