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
)

const cloudflareAPIBaseURL = "https://api.cloudflare.com/client/v4"

type cloudflareAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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

// cloudflareOwnedDomain is a domain already held in the account, as returned by
// the registrar domains list endpoint (distinct from the availability shape).
type cloudflareOwnedDomain struct {
	Name             string `json:"name"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	AutoRenew        bool   `json:"auto_renew"`
	Locked           bool   `json:"locked"`
	CurrentRegistrar string `json:"current_registrar,omitempty"`
	RegistryStatuses string `json:"registry_statuses,omitempty"`
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

func (c *cloudflareClient) ListRegistrarDomains(accountID string) ([]cloudflareOwnedDomain, error) {
	var resp cloudflareAPIResponse[[]cloudflareOwnedDomain]
	endpoint := fmt.Sprintf("/accounts/%s/registrar/domains", accountID)
	if err := c.doJSON(http.MethodGet, endpoint, nil, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, c.apiError("cloudflare registrar domain list failed", resp.Errors)
	}

	return resp.Result, nil
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
		if hint := cloudflareAuthHint(respBody); hint != "" {
			return fmt.Errorf("cloudflare api error (%s): %s\n%s", resp.Status, strings.TrimSpace(string(respBody)), hint)
		}
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

// cloudflareAuthHint inspects a Cloudflare error response body for the
// authorization-format error codes (6003 "Invalid request headers", 6111
// "Invalid format for Authorization header") that signal a malformed or
// wrong-type credential, and returns an actionable hint. The codes can appear
// either at the top level or nested in an error_chain.
func cloudflareAuthHint(body []byte) string {
	var parsed struct {
		Errors []struct {
			Code       int `json:"code"`
			ErrorChain []struct {
				Code int `json:"code"`
			} `json:"error_chain"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return ""
	}
	authError := false
	for _, e := range parsed.Errors {
		if e.Code == 6003 || e.Code == 6111 {
			authError = true
		}
		for _, chained := range e.ErrorChain {
			if chained.Code == 6003 || chained.Code == 6111 {
				authError = true
			}
		}
	}
	if !authError {
		return ""
	}
	return "hint: that credential is not a valid Cloudflare API token. Don't use your Global API Key, account email, or a tunnel connector token here.\n" +
		"      Create a scoped API token at https://dash.cloudflare.com/profile/api-tokens."
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
