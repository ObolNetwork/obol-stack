package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
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
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return tunnel.Status(cfg, getUI(cmd))
				},
			},
			{
				Name:  "setup",
				Usage: "Guided persistent tunnel setup with optional domain registration",
				Flags: tunnelSetupFlags(),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					opts, err := setupOptionsFromCommand(cmd, u)
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
				Name:  "login",
				Usage: "Authenticate via browser and create a locally-managed tunnel (no API token)",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "hostname",
						Aliases:  []string{"H"},
						Usage:    "Public hostname to route (e.g. stack.example.com)",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return tunnel.Login(cfg, getUI(cmd), tunnel.LoginOptions{Hostname: cmd.String("hostname")})
				},
			},
			{
				Name:  "provision",
				Usage: "Provision a persistent remote-managed Cloudflare Tunnel",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "hostname",
						Aliases:  []string{"H"},
						Usage:    "Public hostname to route (e.g. stack.example.com)",
						Required: true,
					},
					&cli.StringFlag{
						Name:    "account-id",
						Aliases: []string{"a"},
						Usage:   "Cloudflare account ID (optional if the API token can access a single account)",
						Sources: cli.EnvVars("CLOUDFLARE_ACCOUNT_ID"),
					},
					&cli.StringFlag{
						Name:    "zone-id",
						Aliases: []string{"z"},
						Usage:   "Cloudflare zone ID for the hostname (auto-detected when omitted)",
						Sources: cli.EnvVars("CLOUDFLARE_ZONE_ID"),
					},
					&cli.StringFlag{
						Name:    "api-token",
						Aliases: []string{"t"},
						Usage:   "Cloudflare API token",
						Sources: cli.EnvVars("CLOUDFLARE_API_TOKEN"),
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return tunnel.Provision(cfg, getUI(cmd), tunnel.ProvisionOptions{
						Hostname:  cmd.String("hostname"),
						AccountID: cmd.String("account-id"),
						ZoneID:    cmd.String("zone-id"),
						APIToken:  cmd.String("api-token"),
					})
				},
			},
			{
				Name:  "restart",
				Usage: "Restart the tunnel connector (quick tunnels get a new URL)",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return tunnel.Restart(cfg, getUI(cmd))
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
		Usage: "Search, check, and register Cloudflare Registrar domains",
		Commands: []*cli.Command{
			{
				Name:  "search",
				Usage: "Search for available Cloudflare Registrar domains",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "query", Aliases: []string{"q"}, Usage: "Keyword, phrase, or domain to search for"},
					&cli.StringSliceFlag{Name: "extensions", Usage: "Optional extension filter(s), e.g. --extensions com --extensions dev"},
					&cli.IntFlag{Name: "limit", Usage: "Maximum number of suggestions to return", Value: 10},
					&cli.StringFlag{Name: "account-id", Aliases: []string{"a"}, Usage: "Cloudflare account ID", Sources: cli.EnvVars("CLOUDFLARE_ACCOUNT_ID")},
					&cli.StringFlag{Name: "api-token", Aliases: []string{"t"}, Usage: "Cloudflare API token", Sources: cli.EnvVars("CLOUDFLARE_API_TOKEN")},
					&cli.StringFlag{Name: "from-json", Usage: "Read search options from JSON file (or - for stdin)"},
				},
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
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "account-id", Aliases: []string{"a"}, Usage: "Cloudflare account ID", Sources: cli.EnvVars("CLOUDFLARE_ACCOUNT_ID")},
					&cli.StringFlag{Name: "api-token", Aliases: []string{"t"}, Usage: "Cloudflare API token", Sources: cli.EnvVars("CLOUDFLARE_API_TOKEN")},
					&cli.StringFlag{Name: "from-json", Usage: "Read check options from JSON file (or - for stdin)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					opts, err := domainCheckOptionsFromCommand(cmd)
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
				Usage:     "Register a domain through Cloudflare Registrar",
				ArgsUsage: "<domain>",
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "years", Usage: "Registration term in years (default 1 or registry minimum)", Value: 1},
					&cli.BoolFlag{Name: "auto-renew", Usage: "Enable automatic renewal"},
					&cli.StringFlag{Name: "privacy-mode", Usage: "WHOIS privacy mode", Value: "redaction"},
					&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "Confirm the billable registration without prompting"},
					&cli.BoolFlag{Name: "respond-async", Usage: "Request an immediate async workflow response from Cloudflare"},
					&cli.StringFlag{Name: "account-id", Aliases: []string{"a"}, Usage: "Cloudflare account ID", Sources: cli.EnvVars("CLOUDFLARE_ACCOUNT_ID")},
					&cli.StringFlag{Name: "api-token", Aliases: []string{"t"}, Usage: "Cloudflare API token", Sources: cli.EnvVars("CLOUDFLARE_API_TOKEN")},
					&cli.StringFlag{Name: "from-json", Usage: "Read registration options from JSON file (or - for stdin)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					u := getUI(cmd)
					opts, err := domainRegisterOptionsFromCommand(cmd)
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

func tunnelSetupFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "hostname", Aliases: []string{"H"}, Usage: "Public hostname to route (e.g. stack.example.com)"},
		&cli.StringFlag{Name: "management", Usage: "Tunnel management mode: local or remote", Value: "auto"},
		&cli.StringFlag{Name: "account-id", Aliases: []string{"a"}, Usage: "Cloudflare account ID", Sources: cli.EnvVars("CLOUDFLARE_ACCOUNT_ID")},
		&cli.StringFlag{Name: "zone-id", Aliases: []string{"z"}, Usage: "Cloudflare zone ID (auto-detected when omitted)", Sources: cli.EnvVars("CLOUDFLARE_ZONE_ID")},
		&cli.StringFlag{Name: "api-token", Aliases: []string{"t"}, Usage: "Cloudflare API token", Sources: cli.EnvVars("CLOUDFLARE_API_TOKEN")},
		&cli.BoolFlag{Name: "register-domain", Usage: "Register the domain apex via Cloudflare Registrar when the zone is missing"},
		&cli.IntFlag{Name: "years", Usage: "Domain registration term in years", Value: 1},
		&cli.BoolFlag{Name: "auto-renew", Usage: "Enable domain auto-renew when registering a domain"},
		&cli.StringFlag{Name: "privacy-mode", Usage: "WHOIS privacy mode for registration", Value: "redaction"},
		&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "Confirm billable domain registration without prompting"},
		&cli.StringFlag{Name: "from-json", Usage: "Read setup options from JSON file (or - for stdin)"},
	}
}

func setupOptionsFromCommand(cmd *cli.Command, u interface {
	Input(string, string) (string, error)
}) (tunnel.SetupOptions, error) {
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

	hostname := cmd.String("hostname")
	if strings.TrimSpace(hostname) == "" {
		input, err := u.Input("Public hostname", "")
		if err != nil {
			return tunnel.SetupOptions{}, err
		}
		hostname = input
	}

	return tunnel.SetupOptions{
		Hostname:       hostname,
		Management:     cmd.String("management"),
		AccountID:      cmd.String("account-id"),
		ZoneID:         cmd.String("zone-id"),
		APIToken:       cmd.String("api-token"),
		RegisterDomain: cmd.Bool("register-domain"),
		Years:          cmd.Int("years"),
		AutoRenew:      cmd.Bool("auto-renew"),
		PrivacyMode:    cmd.String("privacy-mode"),
		ConfirmCharge:  cmd.Bool("yes"),
	}, nil
}

func domainSearchOptionsFromCommand(cmd *cli.Command, u interface {
	Input(string, string) (string, error)
}) (tunnel.DomainSearchOptions, error) {
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

	return tunnel.DomainSearchOptions{
		Query:      query,
		Extensions: cmd.StringSlice("extensions"),
		Limit:      cmd.Int("limit"),
		AccountID:  cmd.String("account-id"),
		APIToken:   cmd.String("api-token"),
	}, nil
}

func domainCheckOptionsFromCommand(cmd *cli.Command) (tunnel.DomainCheckOptions, error) {
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

	return tunnel.DomainCheckOptions{
		Domains:   cmd.Args().Slice(),
		AccountID: cmd.String("account-id"),
		APIToken:  cmd.String("api-token"),
	}, nil
}

func domainRegisterOptionsFromCommand(cmd *cli.Command) (tunnel.DomainRegisterOptions, error) {
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

	return tunnel.DomainRegisterOptions{
		DomainName:    cmd.Args().First(),
		Years:         cmd.Int("years"),
		AutoRenew:     cmd.Bool("auto-renew"),
		PrivacyMode:   cmd.String("privacy-mode"),
		ConfirmCharge: cmd.Bool("yes"),
		RespondAsync:  cmd.Bool("respond-async"),
		AccountID:     cmd.String("account-id"),
		APIToken:      cmd.String("api-token"),
	}, nil
}

func printDomainSuggestions(u interface {
	Blank()
	Bold(string)
	Print(string)
	Detail(string, string)
}, result *tunnel.DomainSearchResult) {
	u.Blank()
	u.Bold("Domain Suggestions")
	for _, domain := range result.Domains {
		summary := "not registrable"
		if domain.Registrable {
			summary = tunnelSummaryPrice(domain)
		}
		if domain.Reason != "" {
			summary = summary + " — " + domain.Reason
		}
		u.Print("- " + domain.Name)
		u.Detail("  Status", summary)
	}
}

func printDomainChecks(u interface {
	Blank()
	Bold(string)
	Print(string)
	Detail(string, string)
}, result *tunnel.DomainCheckResult) {
	u.Blank()
	u.Bold("Domain Availability")
	for _, domain := range result.Domains {
		status := "not registrable"
		if domain.Registrable {
			status = tunnelSummaryPrice(domain)
		}
		if domain.Reason != "" {
			status = status + " — " + domain.Reason
		}
		u.Print("- " + domain.Name)
		u.Detail("  Status", status)
	}
}

func printDomainRegistration(u interface {
	Blank()
	Bold(string)
	Print(string)
	Detail(string, string)
	Successf(string, ...any)
}, result *tunnel.DomainRegisterResult) {
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
