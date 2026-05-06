package tunnel

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractZoneName(t *testing.T) {
	zone, err := extractZoneName("api.stack.example.co.uk")
	if err != nil {
		t.Fatalf("extractZoneName: %v", err)
	}
	if zone != "example.co.uk" {
		t.Fatalf("zone = %q, want example.co.uk", zone)
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

func TestCloudflareClientResolveZoneForHostname(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("name"); got != "example.co.uk" {
			t.Fatalf("zone query = %q, want example.co.uk", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result": []map[string]any{{
				"id":   "zone-123",
				"name": "example.co.uk",
				"account": map[string]any{
					"id":   "acct-123",
					"name": "Main",
				},
			}},
		})
	}))
	defer server.Close()

	client := newCloudflareClient("token")
	client.baseURL = server.URL

	zone, err := client.ResolveZoneForHostname("stack.example.co.uk")
	if err != nil {
		t.Fatalf("ResolveZoneForHostname: %v", err)
	}
	if zone.ID != "zone-123" || zone.Account.ID != "acct-123" {
		t.Fatalf("unexpected zone: %+v", zone)
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
