package tunnel

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudflareAuthHint(t *testing.T) {
	// Nested error_chain code 6111 (the connector-token-as-API-token mistake).
	body := []byte(`{"success":false,"errors":[{"code":6003,"message":"Invalid request headers","error_chain":[{"code":6111,"message":"Invalid format for Authorization header"}]}]}`)
	if hint := cloudflareAuthHint(body); !strings.Contains(hint, "not a valid Cloudflare API token") {
		t.Fatalf("expected auth hint, got %q", hint)
	}

	// An unrelated error must not produce a hint.
	other := []byte(`{"success":false,"errors":[{"code":1003,"message":"record exists"}]}`)
	if hint := cloudflareAuthHint(other); hint != "" {
		t.Fatalf("did not expect a hint for non-auth error, got %q", hint)
	}
}

func TestSaveAndLoadRemoteTunnelToken(t *testing.T) {
	cfg := testConfig(t)
	if err := saveRemoteTunnelToken(cfg, "secret-token"); err != nil {
		t.Fatalf("saveRemoteTunnelToken: %v", err)
	}
	got, err := loadRemoteTunnelToken(cfg)
	if err != nil {
		t.Fatalf("loadRemoteTunnelToken: %v", err)
	}
	if got != "secret-token" {
		t.Fatalf("token = %q, want secret-token", got)
	}
}

func TestCloudflareClientResolveAccountIDSingleAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  []map[string]any{{"id": "acct-123", "name": "Main"}},
		})
	}))
	defer server.Close()

	client := newCloudflareClient("token")
	client.baseURL = server.URL

	accountID, err := client.ResolveAccountID("")
	if err != nil {
		t.Fatalf("ResolveAccountID: %v", err)
	}
	if accountID != "acct-123" {
		t.Fatalf("accountID = %q, want acct-123", accountID)
	}
}

func TestCloudflareClientRegistrarEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/accounts/acct-123/registrar/domain-search":
			if got := r.URL.Query().Get("q"); got != "obol" {
				t.Fatalf("search q = %q, want obol", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": map[string]any{
					"domains": []map[string]any{{
						"name":        "obolstack.dev",
						"registrable": true,
						"tier":        "standard",
						"pricing": map[string]any{
							"currency":          "USD",
							"registration_cost": "10.00",
							"renewal_cost":      "10.00",
						},
					}},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/acct-123/registrar/domain-check":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "obolstack.dev") {
				t.Fatalf("domain-check body missing domain: %s", string(body))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": map[string]any{
					"domains": []map[string]any{{
						"name":        "obolstack.dev",
						"registrable": true,
						"tier":        "standard",
						"pricing": map[string]any{
							"currency":          "USD",
							"registration_cost": "10.00",
							"renewal_cost":      "10.00",
						},
					}},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/acct-123/registrar/registrations":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "obolstack.dev") {
				t.Fatalf("registrations body missing domain: %s", string(body))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": map[string]any{
					"completed": true,
					"state":     "succeeded",
					"links": map[string]any{
						"self":     "https://example.test/workflows/1",
						"resource": "https://example.test/domains/obolstack.dev",
					},
				},
			})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newCloudflareClient("token")
	client.baseURL = server.URL

	search, err := client.SearchRegistrarDomains("acct-123", "obol", 5, []string{"dev"})
	if err != nil {
		t.Fatalf("SearchRegistrarDomains: %v", err)
	}
	if len(search) != 1 || search[0].Name != "obolstack.dev" {
		t.Fatalf("unexpected search results: %+v", search)
	}

	check, err := client.CheckRegistrarDomains("acct-123", []string{"obolstack.dev"})
	if err != nil {
		t.Fatalf("CheckRegistrarDomains: %v", err)
	}
	if len(check) != 1 || !check[0].Registrable {
		t.Fatalf("unexpected check results: %+v", check)
	}

	workflow, err := client.CreateRegistration("acct-123", cloudflareRegistrationRequest{
		DomainName:  "obolstack.dev",
		Years:       1,
		PrivacyMode: "redaction",
	}, false)
	if err != nil {
		t.Fatalf("CreateRegistration: %v", err)
	}
	if workflow.State != "succeeded" {
		t.Fatalf("workflow state = %q, want succeeded", workflow.State)
	}
}
