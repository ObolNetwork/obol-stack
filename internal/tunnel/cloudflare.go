package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

const cloudflareAPIBaseURL = "https://api.cloudflare.com/client/v4"

var errCloudflareZoneNotFound = errors.New("cloudflare zone not found")

type cloudflareTunnel struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name,omitempty"`
	Token       string                       `json:"token,omitempty"`
	Status      string                       `json:"status,omitempty"`
	Connections []cloudflareTunnelConnection `json:"connections,omitempty"`
}

type cloudflareTunnelConnection struct {
	ID string `json:"id,omitempty"`
}

type cloudflareAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cloudflareZone struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Account cloudflareAccount `json:"account"`
}

type cloudflareDomainPricing struct {
	Currency         string `json:"currency,omitempty"`
	RegistrationCost string `json:"registration_cost,omitempty"`
	RenewalCost      string `json:"renewal_cost,omitempty"`
}

type cloudflareRegistrarDomain struct {
	Name        string                   `json:"name"`
	Registrable bool                     `json:"registrable"`
	Pricing     *cloudflareDomainPricing `json:"pricing,omitempty"`
	Reason      string                   `json:"reason,omitempty"`
	Tier        string                   `json:"tier,omitempty"`
}

type cloudflareRegistrationRequest struct {
	DomainName  string `json:"domain_name"`
	AutoRenew   bool   `json:"auto_renew,omitempty"`
	PrivacyMode string `json:"privacy_mode,omitempty"`
	Years       int    `json:"years,omitempty"`
}

type cloudflareWorkflowError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type cloudflareWorkflowLinks struct {
	Self     string `json:"self"`
	Resource string `json:"resource,omitempty"`
}

type cloudflareRegistrarWorkflow struct {
	Completed bool                     `json:"completed"`
	CreatedAt string                   `json:"created_at,omitempty"`
	UpdatedAt string                   `json:"updated_at,omitempty"`
	State     string                   `json:"state"`
	Links     cloudflareWorkflowLinks  `json:"links"`
	Context   map[string]any           `json:"context,omitempty"`
	Error     *cloudflareWorkflowError `json:"error,omitempty"`
}

type dnsRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

type cloudflareClient struct {
	apiToken   string
	baseURL    string
	httpClient *http.Client
}

type cloudflareAPIError struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
}

type cloudflareAPIResponse[T any] struct {
	Success bool                 `json:"success"`
	Errors  []cloudflareAPIError `json:"errors"`
	Result  T                    `json:"result"`
}

func newCloudflareClient(apiToken string) *cloudflareClient {
	return &cloudflareClient{
		apiToken: strings.TrimSpace(apiToken),
		baseURL:  cloudflareAPIBaseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func extractZoneName(hostname string) (string, error) {
	hostname = normalizeHostname(hostname)
	if hostname == "" {
		return "", errors.New("hostname is required")
	}

	zoneName, err := publicsuffix.EffectiveTLDPlusOne(hostname)
	if err != nil {
		return "", fmt.Errorf("derive zone from hostname %q: %w", hostname, err)
	}

	return zoneName, nil
}

func (c *cloudflareClient) ListAccounts() ([]cloudflareAccount, error) {
	var resp cloudflareAPIResponse[[]cloudflareAccount]
	if err := c.doJSON(http.MethodGet, "/accounts", nil, nil, &resp); err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, c.apiError("cloudflare account list failed", resp.Errors)
	}

	return resp.Result, nil
}

func (c *cloudflareClient) ResolveAccountID(explicitAccountID string) (string, error) {
	explicitAccountID = strings.TrimSpace(explicitAccountID)
	if explicitAccountID != "" {
		return explicitAccountID, nil
	}

	accounts, err := c.ListAccounts()
	if err != nil {
		return "", err
	}

	switch len(accounts) {
	case 0:
		return "", errors.New("cloudflare token cannot access any accounts")
	case 1:
		return accounts[0].ID, nil
	default:
		return "", errors.New("--account-id is required because the Cloudflare token can access multiple accounts")
	}
}

func (c *cloudflareClient) ResolveZoneForHostname(hostname string) (*cloudflareZone, error) {
	zoneName, err := extractZoneName(hostname)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("name", zoneName)

	var resp cloudflareAPIResponse[[]cloudflareZone]
	if err := c.doJSON(http.MethodGet, "/zones?"+q.Encode(), nil, nil, &resp); err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, c.apiError("cloudflare zone lookup failed", resp.Errors)
	}

	for _, zone := range resp.Result {
		if strings.EqualFold(zone.Name, zoneName) {
			zoneCopy := zone
			return &zoneCopy, nil
		}
	}

	return nil, fmt.Errorf("%w: %s", errCloudflareZoneNotFound, zoneName)
}

func (c *cloudflareClient) CreateTunnel(accountID, tunnelName string) (*cloudflareTunnel, error) {
	reqBody := map[string]any{
		"name":       tunnelName,
		"config_src": "cloudflare",
	}

	var resp cloudflareAPIResponse[cloudflareTunnel]
	if err := c.doJSON(http.MethodPost, fmt.Sprintf("/accounts/%s/cfd_tunnel", accountID), reqBody, nil, &resp); err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, c.apiError("cloudflare tunnel create failed", resp.Errors)
	}

	return &resp.Result, nil
}

func (c *cloudflareClient) GetTunnel(accountID, tunnelID string) (*cloudflareTunnel, error) {
	var resp cloudflareAPIResponse[cloudflareTunnel]
	if err := c.doJSON(http.MethodGet, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", accountID, tunnelID), nil, nil, &resp); err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, c.apiError("cloudflare tunnel fetch failed", resp.Errors)
	}

	return &resp.Result, nil
}

func (c *cloudflareClient) GetTunnelToken(accountID, tunnelID string) (string, error) {
	var resp cloudflareAPIResponse[string]
	if err := c.doJSON(http.MethodGet, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/token", accountID, tunnelID), nil, nil, &resp); err != nil {
		return "", err
	}

	if !resp.Success || resp.Result == "" {
		return "", c.apiError("cloudflare tunnel token fetch failed", resp.Errors)
	}

	return resp.Result, nil
}

func (c *cloudflareClient) UpdateTunnelConfiguration(accountID, tunnelID, hostname, serviceURL string) error {
	reqBody := map[string]any{
		"config": map[string]any{
			"ingress": []map[string]any{
				{
					"hostname":      hostname,
					"service":       serviceURL,
					"originRequest": map[string]any{},
				},
				{
					"service": "http_status:404",
				},
			},
		},
	}

	var resp cloudflareAPIResponse[map[string]any]
	endpoint := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/configurations", accountID, tunnelID)
	if err := c.doJSON(http.MethodPut, endpoint, reqBody, nil, &resp); err != nil {
		return err
	}

	if !resp.Success {
		return c.apiError("cloudflare tunnel configuration update failed", resp.Errors)
	}

	return nil
}

func (c *cloudflareClient) UpsertTunnelDNSRecord(zoneID, hostname, content string) error {
	q := url.Values{}
	q.Set("type", "CNAME")
	q.Set("name", hostname)

	var listResp cloudflareAPIResponse[[]dnsRecord]
	endpoint := fmt.Sprintf("/zones/%s/dns_records?%s", zoneID, q.Encode())
	if err := c.doJSON(http.MethodGet, endpoint, nil, nil, &listResp); err != nil {
		return err
	}

	if !listResp.Success {
		return c.apiError("cloudflare dns record list failed", listResp.Errors)
	}

	reqBody := map[string]any{
		"type":    "CNAME",
		"proxied": true,
		"name":    hostname,
		"content": content,
	}

	if len(listResp.Result) > 0 {
		var updResp cloudflareAPIResponse[dnsRecord]
		updateEndpoint := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, listResp.Result[0].ID)
		if err := c.doJSON(http.MethodPut, updateEndpoint, reqBody, nil, &updResp); err != nil {
			return err
		}
		if !updResp.Success {
			return c.apiError("cloudflare dns record update failed", updResp.Errors)
		}
		return nil
	}

	var createResp cloudflareAPIResponse[dnsRecord]
	createEndpoint := fmt.Sprintf("/zones/%s/dns_records", zoneID)
	if err := c.doJSON(http.MethodPost, createEndpoint, reqBody, nil, &createResp); err != nil {
		return err
	}
	if !createResp.Success {
		return c.apiError("cloudflare dns record create failed", createResp.Errors)
	}

	return nil
}

func (c *cloudflareClient) SearchRegistrarDomains(accountID, query string, limit int, extensions []string) ([]cloudflareRegistrarDomain, error) {
	q := url.Values{}
	q.Set("q", query)
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	for _, ext := range extensions {
		ext = strings.TrimSpace(strings.TrimPrefix(ext, "."))
		if ext != "" {
			q.Add("extensions", ext)
		}
	}

	var resp cloudflareAPIResponse[struct {
		Domains []cloudflareRegistrarDomain `json:"domains"`
	}]
	endpoint := fmt.Sprintf("/accounts/%s/registrar/domain-search?%s", accountID, q.Encode())
	if err := c.doJSON(http.MethodGet, endpoint, nil, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, c.apiError("cloudflare registrar search failed", resp.Errors)
	}

	return resp.Result.Domains, nil
}

func (c *cloudflareClient) CheckRegistrarDomains(accountID string, domains []string) ([]cloudflareRegistrarDomain, error) {
	if len(domains) == 0 {
		return nil, errors.New("at least one domain is required")
	}
	if len(domains) > 20 {
		return nil, errors.New("cloudflare registrar domain-check supports up to 20 domains per request")
	}

	var resp cloudflareAPIResponse[struct {
		Domains []cloudflareRegistrarDomain `json:"domains"`
	}]
	endpoint := fmt.Sprintf("/accounts/%s/registrar/domain-check", accountID)
	if err := c.doJSON(http.MethodPost, endpoint, map[string]any{"domains": domains}, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, c.apiError("cloudflare registrar availability check failed", resp.Errors)
	}

	return resp.Result.Domains, nil
}

func (c *cloudflareClient) CreateRegistration(accountID string, req cloudflareRegistrationRequest, respondAsync bool) (*cloudflareRegistrarWorkflow, error) {
	headers := map[string]string{}
	if respondAsync {
		headers["Prefer"] = "respond-async"
	}

	var resp cloudflareAPIResponse[cloudflareRegistrarWorkflow]
	endpoint := fmt.Sprintf("/accounts/%s/registrar/registrations", accountID)
	if err := c.doJSON(http.MethodPost, endpoint, req, headers, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, c.apiError("cloudflare domain registration failed", resp.Errors)
	}

	return &resp.Result, nil
}

func (c *cloudflareClient) GetWorkflowStatus(statusURL string) (*cloudflareRegistrarWorkflow, error) {
	var resp cloudflareAPIResponse[cloudflareRegistrarWorkflow]
	if err := c.doJSON(http.MethodGet, statusURL, nil, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, c.apiError("cloudflare workflow status fetch failed", resp.Errors)
	}

	return &resp.Result, nil
}

func (c *cloudflareClient) doJSON(method, endpoint string, reqBody any, headers map[string]string, out any) error {
	var (
		body []byte
		err  error
	)

	if reqBody != nil {
		body, err = json.Marshal(reqBody)
		if err != nil {
			return err
		}
	}

	requestURL := endpoint
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		base, err := url.Parse(strings.TrimRight(c.baseURL, "/") + "/")
		if err != nil {
			return err
		}
		base.Path = path.Join(base.Path, strings.TrimPrefix(endpoint, "/"))
		if strings.Contains(endpoint, "?") {
			parts := strings.SplitN(strings.TrimPrefix(endpoint, "/"), "?", 2)
			base.Path = path.Join(path.Dir(base.Path), path.Base(parts[0]))
			base.RawQuery = parts[1]
		}
		requestURL = base.String()
	}

	req, err := http.NewRequest(method, requestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cloudflare api error (%s): %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return err
	}

	return nil
}

func (c *cloudflareClient) apiError(prefix string, errs []cloudflareAPIError) error {
	if len(errs) == 0 {
		return errors.New(prefix)
	}

	parts := make([]string, 0, len(errs))
	for _, apiErr := range errs {
		parts = append(parts, fmt.Sprintf("[%v] %s", apiErr.Code, apiErr.Message))
	}
	slices.Sort(parts)

	return fmt.Errorf("%s: %s", prefix, strings.Join(parts, "; "))
}
