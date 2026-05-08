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

type SetupOptions struct {
	Hostname          string
	Management        string
	TransportProtocol string
	AccountID         string
	ZoneID            string
	APIToken          string
	RegisterDomain    bool
	Years             int
	AutoRenew         bool
	PrivacyMode       string
	ConfirmCharge     bool
}

type SetupResult struct {
	Hostname           string                       `json:"hostname"`
	URL                string                       `json:"url"`
	Mode               string                       `json:"mode"`
	ManagementMode     string                       `json:"management_mode"`
	TransportProtocol  string                       `json:"transport_protocol,omitempty"`
	AccountID          string                       `json:"account_id,omitempty"`
	ZoneID             string                       `json:"zone_id,omitempty"`
	RegistrationStatus *cloudflareRegistrarWorkflow `json:"registration_status,omitempty"`
}

// Exported aliases for CLI and JSON presentation helpers.
type CloudflareRegistrarDomainAlias = cloudflareRegistrarDomain

type CloudflareRegistrarWorkflowAlias = cloudflareRegistrarWorkflow

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

func Setup(cfg *config.Config, u *ui.UI, opts SetupOptions) (*SetupResult, error) {
	hostname := normalizeHostname(opts.Hostname)
	if hostname == "" {
		return nil, errors.New("--hostname is required (e.g. stack.example.com)")
	}

	management := strings.ToLower(strings.TrimSpace(opts.Management))
	if management == "" || management == "auto" {
		if strings.TrimSpace(opts.APIToken) != "" {
			management = tunnelManagementRemote
		} else {
			management = tunnelManagementLocal
		}
	}

	if management == tunnelManagementLocal {
		if err := Login(cfg, u, LoginOptions{Hostname: hostname, TransportProtocol: opts.TransportProtocol}); err != nil {
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
	if management != tunnelManagementRemote {
		return nil, fmt.Errorf("unsupported tunnel management mode %q", management)
	}
	if strings.TrimSpace(opts.APIToken) == "" {
		return nil, errors.New("--api-token is required for remote tunnel setup")
	}

	client := newCloudflareClient(opts.APIToken)
	zone, err := client.ResolveZoneForHostname(hostname)
	var workflow *cloudflareRegistrarWorkflow
	if err != nil {
		if !errors.Is(err, errCloudflareZoneNotFound) {
			return nil, fmt.Errorf("cloudflare zone lookup failed for %s: %w", hostname, err)
		}
		zoneName, zoneErr := extractZoneName(hostname)
		if zoneErr != nil {
			return nil, zoneErr
		}
		if !opts.RegisterDomain {
			if u.IsTTY() && !u.IsJSON() {
				opts.RegisterDomain = u.Confirm(fmt.Sprintf("Cloudflare does not have zone %s. Register it through Cloudflare Registrar now?", zoneName), false)
			}
		}
		if !opts.RegisterDomain {
			return nil, fmt.Errorf("could not resolve a Cloudflare zone for %s: %w. Add the domain to Cloudflare first or rerun with --register-domain", hostname, err)
		}

		registerResult, regErr := RegisterDomain(u, DomainRegisterOptions{
			DomainName:    zoneName,
			Years:         opts.Years,
			AutoRenew:     opts.AutoRenew,
			PrivacyMode:   opts.PrivacyMode,
			ConfirmCharge: opts.ConfirmCharge,
			AccountID:     opts.AccountID,
			APIToken:      opts.APIToken,
		})
		if regErr != nil {
			return nil, regErr
		}
		workflow = registerResult.Workflow
		zone, err = waitForZone(client, hostname, 12, 5*time.Second)
		if err != nil {
			return nil, fmt.Errorf("domain registration started, but the Cloudflare zone is not ready yet: %w", err)
		}
		if opts.AccountID == "" {
			opts.AccountID = registerResult.AccountID
		}
	}

	if zone != nil {
		if opts.AccountID == "" {
			opts.AccountID = zone.Account.ID
		}
		if opts.ZoneID == "" {
			opts.ZoneID = zone.ID
		}
	}

	if err := Provision(cfg, u, ProvisionOptions{
		Hostname:          hostname,
		AccountID:         opts.AccountID,
		ZoneID:            opts.ZoneID,
		APIToken:          opts.APIToken,
		TransportProtocol: opts.TransportProtocol,
	}); err != nil {
		return nil, err
	}

	return &SetupResult{
		Hostname:           hostname,
		URL:                "https://" + hostname,
		Mode:               tunnelExposurePersistent,
		ManagementMode:     tunnelManagementRemote,
		TransportProtocol:  normalizeTunnelTransportProtocol(opts.TransportProtocol),
		AccountID:          opts.AccountID,
		ZoneID:             opts.ZoneID,
		RegistrationStatus: workflow,
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

func waitForZone(client *cloudflareClient, hostname string, attempts int, delay time.Duration) (*cloudflareZone, error) {
	var lastErr error
	for range attempts {
		zone, err := client.ResolveZoneForHostname(hostname)
		if err == nil {
			return zone, nil
		}
		lastErr = err
		time.Sleep(delay)
	}
	if lastErr == nil {
		lastErr = errors.New("zone not found")
	}
	return nil, lastErr
}
