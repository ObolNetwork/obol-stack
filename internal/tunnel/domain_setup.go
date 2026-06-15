package tunnel

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

type DomainSearchOptions struct {
	Query      string
	Extensions []string
	Limit      int
	AccountID  string
	APIToken   string
}

type DomainSearchResult struct {
	AccountID string                      `json:"account_id"`
	Domains   []cloudflareRegistrarDomain `json:"domains"`
}

type DomainCheckOptions struct {
	Domains   []string
	AccountID string
	APIToken  string
}

type DomainCheckResult struct {
	AccountID string                      `json:"account_id"`
	Domains   []cloudflareRegistrarDomain `json:"domains"`
}

type DomainRegisterOptions struct {
	DomainName    string
	Years         int
	AutoRenew     bool
	PrivacyMode   string
	ConfirmCharge bool
	RespondAsync  bool
	AccountID     string
	APIToken      string
}

type DomainRegisterResult struct {
	AccountID    string                       `json:"account_id"`
	Availability cloudflareRegistrarDomain    `json:"availability"`
	Workflow     *cloudflareRegistrarWorkflow `json:"workflow,omitempty"`
}

// setupManagementLocal routes `obol tunnel setup --management local` to the
// browser-based local-managed flow (an advanced fallback). The default and
// recommended path is the connector-token flow, which needs no host binary and
// no account-wide API token.
const setupManagementLocal = "local"

type SetupOptions struct {
	Hostname   string
	Management string // "" / "connector" (default) or "local" (browser fallback)

	// ConnectorToken is a Cloudflare Tunnel connector token from the dashboard
	// (Networks → Tunnels). Accepts the bare token or the full
	// `cloudflared tunnel run --token …` line — the prefix is stripped.
	ConnectorToken string

	TransportProtocol string

	// OverwriteDNS only applies to the local (browser) fallback; it forwards
	// --overwrite-dns to `cloudflared tunnel route dns`.
	OverwriteDNS bool
}

type SetupResult struct {
	Hostname          string `json:"hostname"`
	URL               string `json:"url"`
	Mode              string `json:"mode"`
	ManagementMode    string `json:"management_mode"`
	TransportProtocol string `json:"transport_protocol,omitempty"`
}

// Exported aliases for CLI and JSON presentation helpers.
type CloudflareRegistrarDomainAlias = cloudflareRegistrarDomain

type CloudflareRegistrarWorkflowAlias = cloudflareRegistrarWorkflow

// Setup is the single guided command for creating a permanent public URL. By
// default it wires a dashboard-managed Cloudflare Tunnel from a connector token
// (least privilege, no host binary). `--management local` delegates to the
// browser-based local-managed fallback.
func Setup(cfg *config.Config, u *ui.UI, opts SetupOptions) (*SetupResult, error) {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		if !u.IsTTY() || u.IsJSON() {
			return nil, errors.New("--hostname is required (e.g. stack.example.com)")
		}
		input, err := u.Input("Public hostname (e.g. stack.example.com)", "")
		if err != nil {
			return nil, err
		}
		hostname = normalizeHostname(input)
	}
	if hostname == "" {
		return nil, errors.New("--hostname is required (e.g. stack.example.com)")
	}

	if strings.EqualFold(strings.TrimSpace(opts.Management), setupManagementLocal) {
		if err := Login(cfg, u, LoginOptions{
			Hostname:          hostname,
			TransportProtocol: opts.TransportProtocol,
			OverwriteDNS:      opts.OverwriteDNS,
		}); err != nil {
			return nil, err
		}
		return &SetupResult{
			Hostname:          hostname,
			URL:               "https://" + hostname,
			Mode:              tunnelExposurePersistent,
			ManagementMode:    tunnelManagementLocal,
			TransportProtocol: normalizeTunnelTransportProtocol(opts.TransportProtocol),
		}, nil
	}

	connectorToken, err := resolveConnectorToken(u, hostname, opts.ConnectorToken)
	if err != nil {
		return nil, err
	}

	if err := ProvisionWithToken(cfg, u, TokenProvisionOptions{
		Hostname:          hostname,
		ConnectorToken:    connectorToken,
		TransportProtocol: opts.TransportProtocol,
	}); err != nil {
		return nil, err
	}

	return &SetupResult{
		Hostname:          hostname,
		URL:               "https://" + hostname,
		Mode:              tunnelExposurePersistent,
		ManagementMode:    tunnelManagementRemote,
		TransportProtocol: normalizeTunnelTransportProtocol(opts.TransportProtocol),
	}, nil
}

// resolveConnectorToken returns a validated connector token from the supplied
// value or, in an interactive session, by walking the user through the
// Cloudflare dashboard steps and prompting for it.
func resolveConnectorToken(u *ui.UI, hostname, supplied string) (string, error) {
	if token := extractConnectorToken(supplied); token != "" {
		if _, err := parseConnectorToken(token); err != nil {
			return "", err
		}
		return token, nil
	}

	if !u.IsTTY() || u.IsJSON() {
		return "", errors.New("a Cloudflare connector token is required: pass it as 'obol tunnel setup <token>' or via --token")
	}

	printConnectorSetupSteps(u, hostname)
	input, err := u.Input("Paste the connector token (or the whole 'cloudflared tunnel run --token …' line)", "")
	if err != nil {
		return "", err
	}
	token := extractConnectorToken(input)
	if token == "" {
		return "", errors.New("no connector token found in the pasted value")
	}
	if _, err := parseConnectorToken(token); err != nil {
		return "", err
	}
	return token, nil
}

// printConnectorSetupSteps prints the Cloudflare dashboard walkthrough, including
// the exact in-cluster Service URI the user must route the public hostname to.
func printConnectorSetupSteps(u *ui.UI, hostname string) {
	u.Blank()
	u.Bold("Create a permanent tunnel in the Cloudflare dashboard")
	u.Print("A tunnel exposes your stack to the public internet so buyers can discover and pay")
	u.Print("for the services you sell. You only need this once you're ready to sell.")
	u.Blank()
	u.Print("  1. Open https://one.dash.cloudflare.com → Networks → Tunnels → Create a tunnel")
	u.Print("  2. Choose 'Cloudflared', name the tunnel, and save.")
	u.Print("  3. On the install screen, copy the token (the long eyJ… value).")
	u.Dim("     You do NOT run the command it shows — Obol runs the connector for you.")
	u.Print("  4. Open the 'Public Hostname' tab → Add a public hostname:")
	u.Detail("       Subdomain / Domain", hostname)
	u.Detail("       Type", "HTTP")
	u.Detail("       Service URL", "http://traefik.traefik.svc.cluster.local:80")
	u.Print("  5. Save the public hostname.")
	u.Blank()
}

func SearchDomains(opts DomainSearchOptions) (*DomainSearchResult, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil, errors.New("domain search query is required")
	}
	if opts.APIToken == "" {
		return nil, errors.New("--api-token is required (or set CLOUDFLARE_API_TOKEN)")
	}

	client := newCloudflareClient(opts.APIToken)
	accountID, err := client.ResolveAccountID(opts.AccountID)
	if err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	domains, err := client.SearchRegistrarDomains(accountID, query, limit, opts.Extensions)
	if err != nil {
		return nil, err
	}

	return &DomainSearchResult{AccountID: accountID, Domains: domains}, nil
}

func CheckDomains(opts DomainCheckOptions) (*DomainCheckResult, error) {
	if len(opts.Domains) == 0 {
		return nil, errors.New("at least one domain is required")
	}
	if opts.APIToken == "" {
		return nil, errors.New("--api-token is required (or set CLOUDFLARE_API_TOKEN)")
	}

	normalized := make([]string, 0, len(opts.Domains))
	for _, domain := range opts.Domains {
		domain = normalizeHostname(domain)
		if domain != "" {
			normalized = append(normalized, domain)
		}
	}
	if len(normalized) == 0 {
		return nil, errors.New("at least one valid domain is required")
	}

	client := newCloudflareClient(opts.APIToken)
	accountID, err := client.ResolveAccountID(opts.AccountID)
	if err != nil {
		return nil, err
	}

	domains, err := client.CheckRegistrarDomains(accountID, normalized)
	if err != nil {
		return nil, err
	}

	return &DomainCheckResult{AccountID: accountID, Domains: domains}, nil
}

func RegisterDomain(u *ui.UI, opts DomainRegisterOptions) (*DomainRegisterResult, error) {
	domainName := normalizeHostname(opts.DomainName)
	if domainName == "" {
		return nil, errors.New("domain name is required")
	}
	if opts.APIToken == "" {
		return nil, errors.New("--api-token is required (or set CLOUDFLARE_API_TOKEN)")
	}

	privacyMode := strings.TrimSpace(opts.PrivacyMode)
	if privacyMode == "" {
		privacyMode = "redaction"
	}

	client := newCloudflareClient(opts.APIToken)
	accountID, err := client.ResolveAccountID(opts.AccountID)
	if err != nil {
		return nil, err
	}

	check, err := CheckDomains(DomainCheckOptions{
		Domains:   []string{domainName},
		AccountID: accountID,
		APIToken:  opts.APIToken,
	})
	if err != nil {
		return nil, err
	}

	availability, err := findDomainAvailability(check.Domains, domainName)
	if err != nil {
		return nil, err
	}
	if !availability.Registrable {
		return nil, fmt.Errorf("%s is not registrable via the Cloudflare API (%s)", domainName, availability.Reason)
	}
	if strings.EqualFold(availability.Tier, "premium") {
		return nil, fmt.Errorf("%s is a premium domain and cannot be registered via the Cloudflare API yet", domainName)
	}

	priceText := formatDomainPricing(availability)
	if !opts.ConfirmCharge {
		if !u.IsTTY() || u.IsJSON() {
			return nil, errors.New("domain registration is billable; rerun with --yes to confirm the charge")
		}
		if !u.Confirm(fmt.Sprintf("Register %s for %s? This is billable and non-refundable.", domainName, priceText), false) {
			return nil, errors.New("domain registration cancelled")
		}
	}

	workflow, err := client.CreateRegistration(accountID, cloudflareRegistrationRequest{
		DomainName:  domainName,
		Years:       opts.Years,
		AutoRenew:   opts.AutoRenew,
		PrivacyMode: privacyMode,
	}, opts.RespondAsync)
	if err != nil {
		return nil, err
	}

	if workflow != nil && !workflow.Completed && workflow.Links.Self != "" {
		workflow, err = waitForWorkflow(client, workflow, 6, 5*time.Second)
		if err != nil {
			return nil, err
		}
	}
	if workflow != nil {
		switch workflow.State {
		case "failed":
			if workflow.Error != nil && workflow.Error.Message != "" {
				return nil, fmt.Errorf("domain registration failed: %s", workflow.Error.Message)
			}
			return nil, errors.New("domain registration failed")
		case "action_required", "blocked":
			message := fmt.Sprintf("domain registration requires manual action in Cloudflare (%s)", workflow.State)
			if workflow.Error != nil && workflow.Error.Message != "" {
				message = fmt.Sprintf("domain registration requires manual action in Cloudflare: %s", workflow.Error.Message)
			}
			if workflow.Links.Self != "" {
				message += ": " + workflow.Links.Self
			}
			return nil, errors.New(message)
		}
	}

	return &DomainRegisterResult{
		AccountID:    accountID,
		Availability: availability,
		Workflow:     workflow,
	}, nil
}

func findDomainAvailability(domains []cloudflareRegistrarDomain, domainName string) (cloudflareRegistrarDomain, error) {
	for _, domain := range domains {
		if strings.EqualFold(domain.Name, domainName) {
			return domain, nil
		}
	}
	return cloudflareRegistrarDomain{}, fmt.Errorf("cloudflare did not return availability data for %s", domainName)
}

func formatDomainPricing(domain cloudflareRegistrarDomain) string {
	if domain.Pricing == nil || domain.Pricing.RegistrationCost == "" {
		return "current registry pricing"
	}
	if domain.Pricing.Currency == "" {
		return domain.Pricing.RegistrationCost
	}
	return fmt.Sprintf("%s %s/year", domain.Pricing.Currency, domain.Pricing.RegistrationCost)
}

func waitForWorkflow(client *cloudflareClient, workflow *cloudflareRegistrarWorkflow, attempts int, delay time.Duration) (*cloudflareRegistrarWorkflow, error) {
	current := workflow
	for range attempts {
		if current == nil || current.Completed || current.Links.Self == "" {
			return current, nil
		}
		switch current.State {
		case "action_required", "blocked", "failed", "succeeded":
			return current, nil
		}
		time.Sleep(delay)
		next, err := client.GetWorkflowStatus(current.Links.Self)
		if err != nil {
			return current, err
		}
		current = next
	}
	return current, nil
}
