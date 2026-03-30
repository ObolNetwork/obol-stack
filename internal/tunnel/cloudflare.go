package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type cloudflareTunnel struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

type cloudflareClient struct {
	apiToken string
}

func newCloudflareClient(apiToken string) *cloudflareClient {
	return &cloudflareClient{apiToken: apiToken}
}

func (c *cloudflareClient) CreateTunnel(accountID, tunnelName string) (*cloudflareTunnel, error) {
	reqBody := map[string]any{
		"name":       tunnelName,
		"config_src": "cloudflare",
	}

	var resp struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		} `json:"result"`
	}

	if err := c.doJSON("POST", fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/cfd_tunnel", accountID), reqBody, &resp); err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("cloudflare tunnel create failed: %v", resp.Errors)
	}

	return &cloudflareTunnel{ID: resp.Result.ID, Token: resp.Result.Token}, nil
}

func (c *cloudflareClient) GetTunnelToken(accountID, tunnelID string) (string, error) {
	var resp struct {
		Success bool   `json:"success"`
		Errors  []any  `json:"errors"`
		Result  string `json:"result"`
	}

	if err := c.doJSON("GET", fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/cfd_tunnel/%s/token", accountID, tunnelID), nil, &resp); err != nil {
		return "", err
	}

	if !resp.Success || resp.Result == "" {
		return "", errors.New("cloudflare tunnel token fetch failed")
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

	var resp struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}

	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/cfd_tunnel/%s/configurations", accountID, tunnelID)
	if err := c.doJSON("PUT", url, reqBody, &resp); err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("cloudflare tunnel configuration update failed: %v", resp.Errors)
	}

	return nil
}

type dnsRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

func (c *cloudflareClient) UpsertTunnelDNSRecord(zoneID, hostname, content string) error {
	// Find existing records for this exact name/type.
	listURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?type=CNAME&name=%s", zoneID, url.QueryEscape(hostname))

	var listResp struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result []dnsRecord `json:"result"`
	}
	if err := c.doJSON("GET", listURL, nil, &listResp); err != nil {
		return err
	}

	if !listResp.Success {
		return fmt.Errorf("cloudflare dns record list failed: %v", listResp.Errors)
	}

	if len(listResp.Result) > 0 {
		// Update first matching record.
		recID := listResp.Result[0].ID
		updateURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", zoneID, recID)
		reqBody := map[string]any{
			"type":    "CNAME",
			"proxied": true,
			"name":    hostname,
			"content": content,
		}

		var updResp struct {
			Success bool `json:"success"`
			Errors  []struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := c.doJSON("PUT", updateURL, reqBody, &updResp); err != nil {
			return err
		}

		if !updResp.Success {
			return fmt.Errorf("cloudflare dns record update failed: %v", updResp.Errors)
		}

		return nil
	}

	// Create new record.
	createURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID)
	reqBody := map[string]any{
		"type":    "CNAME",
		"proxied": true,
		"name":    hostname,
		"content": content,
	}

	var createResp struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := c.doJSON("POST", createURL, reqBody, &createResp); err != nil {
		return err
	}

	if !createResp.Success {
		return fmt.Errorf("cloudflare dns record create failed: %v", createResp.Errors)
	}

	return nil
}

func (c *cloudflareClient) doJSON(method, url string, reqBody any, out any) error {
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

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Best effort: surface body for debugging without leaking secrets.
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
