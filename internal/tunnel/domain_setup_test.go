package tunnel

import (
	"errors"
	"strings"
	"testing"
)

type fakeSetupUI struct {
	tty           bool
	json          bool
	confirmResult bool
	secretInput   string
	secretErr     error
	selectIndex   int
	selectErr     error

	confirmPrompts []string
	secretPrompts  []string
	selectPrompts  []string
	selectOptions  [][]string
	selectDefaults []int
}

func (f *fakeSetupUI) IsTTY() bool { return f.tty }

func (f *fakeSetupUI) IsJSON() bool { return f.json }

func (f *fakeSetupUI) Confirm(msg string, defaultYes bool) bool {
	f.confirmPrompts = append(f.confirmPrompts, msg)
	return f.confirmResult
}

func (f *fakeSetupUI) SecretInput(msg string) (string, error) {
	f.secretPrompts = append(f.secretPrompts, msg)
	return f.secretInput, f.secretErr
}

func (f *fakeSetupUI) Select(msg string, options []string, defaultIdx int) (int, error) {
	f.selectPrompts = append(f.selectPrompts, msg)
	f.selectOptions = append(f.selectOptions, append([]string(nil), options...))
	f.selectDefaults = append(f.selectDefaults, defaultIdx)
	return f.selectIndex, f.selectErr
}

type fakeSetupCloudflareClient struct {
	zone        *cloudflareZone
	zoneErr     error
	zoneLookups int
	accounts    []cloudflareAccount
	accountsErr error
}

func (f *fakeSetupCloudflareClient) ResolveZoneForHostname(hostname string) (*cloudflareZone, error) {
	f.zoneLookups++
	if f.zoneErr != nil {
		return nil, f.zoneErr
	}
	return f.zone, nil
}

func (f *fakeSetupCloudflareClient) ListAccounts() ([]cloudflareAccount, error) {
	if f.accountsErr != nil {
		return nil, f.accountsErr
	}
	return append([]cloudflareAccount(nil), f.accounts...), nil
}

func TestResolveSetupManagementInteractiveAutoPrompts(t *testing.T) {
	ui := &fakeSetupUI{tty: true, selectIndex: 0}

	management, err := resolveSetupManagement(ui, "auto", "")
	if err != nil {
		t.Fatalf("resolveSetupManagement: %v", err)
	}
	if management != tunnelManagementRemote {
		t.Fatalf("management = %q, want %q", management, tunnelManagementRemote)
	}
	if len(ui.selectPrompts) != 1 {
		t.Fatalf("select prompts = %d, want 1", len(ui.selectPrompts))
	}
	if ui.selectDefaults[0] != 1 {
		t.Fatalf("default selection = %d, want 1 (local heuristic without token)", ui.selectDefaults[0])
	}
}

func TestResolveSetupManagementNonInteractiveUsesTokenHeuristic(t *testing.T) {
	ui := &fakeSetupUI{}

	management, err := resolveSetupManagement(ui, "auto", "cf-token")
	if err != nil {
		t.Fatalf("resolveSetupManagement: %v", err)
	}
	if management != tunnelManagementRemote {
		t.Fatalf("management = %q, want %q", management, tunnelManagementRemote)
	}
}

func TestResolveSetupManagementRejectsInvalidMode(t *testing.T) {
	ui := &fakeSetupUI{}

	_, err := resolveSetupManagement(ui, "sideways", "")
	if err == nil {
		t.Fatal("expected invalid management mode error")
	}
	if !strings.Contains(err.Error(), "unsupported tunnel management mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveRemoteSetupAPITokenPromptsInteractively(t *testing.T) {
	ui := &fakeSetupUI{tty: true, secretInput: "cf-secret-token"}

	token, err := resolveRemoteSetupAPIToken(ui, "")
	if err != nil {
		t.Fatalf("resolveRemoteSetupAPIToken: %v", err)
	}
	if token != "cf-secret-token" {
		t.Fatalf("token = %q, want cf-secret-token", token)
	}
	if len(ui.secretPrompts) != 1 {
		t.Fatalf("secret prompts = %d, want 1", len(ui.secretPrompts))
	}
}

func TestResolveRemoteSetupAPITokenReturnsExistingValue(t *testing.T) {
	ui := &fakeSetupUI{tty: true}

	token, err := resolveRemoteSetupAPIToken(ui, "already-set")
	if err != nil {
		t.Fatalf("resolveRemoteSetupAPIToken: %v", err)
	}
	if token != "already-set" {
		t.Fatalf("token = %q, want already-set", token)
	}
	if len(ui.secretPrompts) != 0 {
		t.Fatalf("secret prompts = %d, want 0", len(ui.secretPrompts))
	}
}

func TestResolveRemoteSetupAPITokenNonInteractiveRequiresFlag(t *testing.T) {
	ui := &fakeSetupUI{}

	_, err := resolveRemoteSetupAPIToken(ui, "")
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "--api-token is required for remote tunnel setup") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSetupAccountIDSelectsInteractiveAccount(t *testing.T) {
	ui := &fakeSetupUI{tty: true, selectIndex: 1}
	client := &fakeSetupCloudflareClient{accounts: []cloudflareAccount{
		{ID: "acct-dev", Name: "Development"},
		{ID: "acct-prod", Name: "Production"},
	}}

	accountID, err := resolveSetupAccountID(ui, client, "")
	if err != nil {
		t.Fatalf("resolveSetupAccountID: %v", err)
	}
	if accountID != "acct-prod" {
		t.Fatalf("accountID = %q, want acct-prod", accountID)
	}
	if len(ui.selectPrompts) != 1 {
		t.Fatalf("select prompts = %d, want 1", len(ui.selectPrompts))
	}
}

func TestResolveSetupAccountIDReturnsExplicitID(t *testing.T) {
	ui := &fakeSetupUI{tty: true}
	client := &fakeSetupCloudflareClient{}

	accountID, err := resolveSetupAccountID(ui, client, "acct-explicit")
	if err != nil {
		t.Fatalf("resolveSetupAccountID: %v", err)
	}
	if accountID != "acct-explicit" {
		t.Fatalf("accountID = %q, want acct-explicit", accountID)
	}
}

func TestResolveSetupAccountIDReturnsSingleAccessibleAccount(t *testing.T) {
	ui := &fakeSetupUI{}
	client := &fakeSetupCloudflareClient{accounts: []cloudflareAccount{{ID: "acct-solo", Name: "Only"}}}

	accountID, err := resolveSetupAccountID(ui, client, "")
	if err != nil {
		t.Fatalf("resolveSetupAccountID: %v", err)
	}
	if accountID != "acct-solo" {
		t.Fatalf("accountID = %q, want acct-solo", accountID)
	}
}

func TestResolveSetupAccountIDNonInteractiveErrorsOnMultipleAccounts(t *testing.T) {
	ui := &fakeSetupUI{}
	client := &fakeSetupCloudflareClient{accounts: []cloudflareAccount{
		{ID: "acct-dev", Name: "Development"},
		{ID: "acct-prod", Name: "Production"},
	}}

	_, err := resolveSetupAccountID(ui, client, "")
	if err == nil {
		t.Fatal("expected multiple-account error")
	}
	if !strings.Contains(err.Error(), "--account-id is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveRemoteSetupOptionsUsesExplicitZoneWithoutLookup(t *testing.T) {
	ui := &fakeSetupUI{}
	client := &fakeSetupCloudflareClient{}

	got, workflow, err := resolveRemoteSetupOptions(ui, client, "stack.example.com", SetupOptions{
		Hostname:  "stack.example.com",
		AccountID: "acct-123",
		ZoneID:    "zone-123",
	}, func(DomainRegisterOptions) (*DomainRegisterResult, error) {
		t.Fatal("registerDomain should not be called")
		return nil, nil
	}, func(string) (*cloudflareZone, error) {
		t.Fatal("waitForZone should not be called")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("resolveRemoteSetupOptions: %v", err)
	}
	if workflow != nil {
		t.Fatalf("workflow = %+v, want nil", workflow)
	}
	if client.zoneLookups != 0 {
		t.Fatalf("zone lookups = %d, want 0", client.zoneLookups)
	}
	if got.AccountID != "acct-123" || got.ZoneID != "zone-123" {
		t.Fatalf("unexpected options: %+v", got)
	}
}

func TestResolveRemoteSetupOptionsFallsBackToSelectedAccountWhenZoneIDProvided(t *testing.T) {
	ui := &fakeSetupUI{tty: true, selectIndex: 1}
	client := &fakeSetupCloudflareClient{
		zoneErr: errCloudflareZoneNotFound,
		accounts: []cloudflareAccount{
			{ID: "acct-dev", Name: "Development"},
			{ID: "acct-prod", Name: "Production"},
		},
	}

	got, workflow, err := resolveRemoteSetupOptions(ui, client, "stack.example.com", SetupOptions{
		Hostname: "stack.example.com",
		ZoneID:   "zone-123",
	}, func(DomainRegisterOptions) (*DomainRegisterResult, error) {
		t.Fatal("registerDomain should not be called")
		return nil, nil
	}, func(string) (*cloudflareZone, error) {
		t.Fatal("waitForZone should not be called")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("resolveRemoteSetupOptions: %v", err)
	}
	if workflow != nil {
		t.Fatalf("workflow = %+v, want nil", workflow)
	}
	if client.zoneLookups != 1 {
		t.Fatalf("zone lookups = %d, want 1", client.zoneLookups)
	}
	if got.AccountID != "acct-prod" {
		t.Fatalf("accountID = %q, want acct-prod", got.AccountID)
	}
}

func TestResolveRemoteSetupOptionsFallsBackToSingleAccountWhenZoneIDProvided(t *testing.T) {
	ui := &fakeSetupUI{}
	client := &fakeSetupCloudflareClient{
		zoneErr:  errCloudflareZoneNotFound,
		accounts: []cloudflareAccount{{ID: "acct-solo", Name: "Only"}},
	}

	got, workflow, err := resolveRemoteSetupOptions(ui, client, "stack.example.com", SetupOptions{
		Hostname: "stack.example.com",
		ZoneID:   "zone-123",
	}, func(DomainRegisterOptions) (*DomainRegisterResult, error) {
		t.Fatal("registerDomain should not be called")
		return nil, nil
	}, func(string) (*cloudflareZone, error) {
		t.Fatal("waitForZone should not be called")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("resolveRemoteSetupOptions: %v", err)
	}
	if workflow != nil {
		t.Fatalf("workflow = %+v, want nil", workflow)
	}
	if client.zoneLookups != 1 {
		t.Fatalf("zone lookups = %d, want 1", client.zoneLookups)
	}
	if got.AccountID != "acct-solo" {
		t.Fatalf("accountID = %q, want acct-solo", got.AccountID)
	}
}

func TestResolveRemoteSetupOptionsBackfillsAccountFromLookupWhenZoneIDProvided(t *testing.T) {
	ui := &fakeSetupUI{}
	client := &fakeSetupCloudflareClient{zone: &cloudflareZone{
		ID:   "zone-resolved",
		Name: "example.com",
		Account: cloudflareAccount{
			ID:   "acct-from-zone",
			Name: "Authoritative",
		},
	}}

	got, workflow, err := resolveRemoteSetupOptions(ui, client, "stack.example.com", SetupOptions{
		Hostname: "stack.example.com",
		ZoneID:   "zone-explicit",
	}, func(DomainRegisterOptions) (*DomainRegisterResult, error) {
		t.Fatal("registerDomain should not be called")
		return nil, nil
	}, func(string) (*cloudflareZone, error) {
		t.Fatal("waitForZone should not be called")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("resolveRemoteSetupOptions: %v", err)
	}
	if workflow != nil {
		t.Fatalf("workflow = %+v, want nil", workflow)
	}
	if client.zoneLookups != 1 {
		t.Fatalf("zone lookups = %d, want 1", client.zoneLookups)
	}
	if got.AccountID != "acct-from-zone" {
		t.Fatalf("accountID = %q, want acct-from-zone", got.AccountID)
	}
	if got.ZoneID != "zone-explicit" {
		t.Fatalf("zoneID = %q, want zone-explicit", got.ZoneID)
	}
}

func TestResolveRemoteSetupOptionsBackfillsZoneAndAccountFromLookup(t *testing.T) {
	ui := &fakeSetupUI{}
	client := &fakeSetupCloudflareClient{zone: &cloudflareZone{
		ID:   "zone-123",
		Name: "example.com",
		Account: cloudflareAccount{
			ID:   "acct-123",
			Name: "Main",
		},
	}}

	got, workflow, err := resolveRemoteSetupOptions(ui, client, "stack.example.com", SetupOptions{
		Hostname: "stack.example.com",
	}, func(DomainRegisterOptions) (*DomainRegisterResult, error) {
		t.Fatal("registerDomain should not be called")
		return nil, nil
	}, func(string) (*cloudflareZone, error) {
		t.Fatal("waitForZone should not be called")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("resolveRemoteSetupOptions: %v", err)
	}
	if workflow != nil {
		t.Fatalf("workflow = %+v, want nil", workflow)
	}
	if got.AccountID != "acct-123" || got.ZoneID != "zone-123" {
		t.Fatalf("unexpected options: %+v", got)
	}
}

func TestResolveRemoteSetupOptionsRegistersMissingZoneWithSelectedAccount(t *testing.T) {
	ui := &fakeSetupUI{tty: true, confirmResult: true, selectIndex: 1}
	client := &fakeSetupCloudflareClient{
		zoneErr: errCloudflareZoneNotFound,
		accounts: []cloudflareAccount{
			{ID: "acct-dev", Name: "Development"},
			{ID: "acct-prod", Name: "Production"},
		},
	}

	var registered DomainRegisterOptions
	got, workflow, err := resolveRemoteSetupOptions(ui, client, "stack.example.com", SetupOptions{
		Hostname: "stack.example.com",
		Years:    1,
	}, func(opts DomainRegisterOptions) (*DomainRegisterResult, error) {
		registered = opts
		return &DomainRegisterResult{
			AccountID: opts.AccountID,
			Workflow: &cloudflareRegistrarWorkflow{
				Completed: true,
				State:     "succeeded",
			},
		}, nil
	}, func(string) (*cloudflareZone, error) {
		return &cloudflareZone{ID: "zone-123", Name: "example.com", Account: cloudflareAccount{ID: registered.AccountID, Name: "Production"}}, nil
	})
	if err != nil {
		t.Fatalf("resolveRemoteSetupOptions: %v", err)
	}
	if registered.AccountID != "acct-prod" {
		t.Fatalf("registered account = %q, want acct-prod", registered.AccountID)
	}
	if got.AccountID != "acct-prod" || got.ZoneID != "zone-123" {
		t.Fatalf("unexpected options: %+v", got)
	}
	if workflow == nil || workflow.State != "succeeded" {
		t.Fatalf("unexpected workflow: %+v", workflow)
	}
}

func TestResolveRemoteSetupOptionsErrorsWhenRegistrationDeclined(t *testing.T) {
	ui := &fakeSetupUI{tty: true, confirmResult: false}
	client := &fakeSetupCloudflareClient{zoneErr: errCloudflareZoneNotFound}

	_, _, err := resolveRemoteSetupOptions(ui, client, "stack.example.com", SetupOptions{
		Hostname: "stack.example.com",
	}, func(DomainRegisterOptions) (*DomainRegisterResult, error) {
		t.Fatal("registerDomain should not be called")
		return nil, nil
	}, func(string) (*cloudflareZone, error) {
		t.Fatal("waitForZone should not be called")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected missing-zone error")
	}
	if !strings.Contains(err.Error(), "rerun with --register-domain") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveRemoteSetupOptionsPropagatesAccountListErrors(t *testing.T) {
	ui := &fakeSetupUI{tty: true}
	client := &fakeSetupCloudflareClient{
		accountsErr: errors.New("account list failed"),
	}

	_, _, err := resolveRemoteSetupOptions(ui, client, "stack.example.com", SetupOptions{
		Hostname: "stack.example.com",
		ZoneID:   "zone-123",
	}, func(DomainRegisterOptions) (*DomainRegisterResult, error) {
		t.Fatal("registerDomain should not be called")
		return nil, nil
	}, func(string) (*cloudflareZone, error) {
		t.Fatal("waitForZone should not be called")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected account list error")
	}
	if !strings.Contains(err.Error(), "account list failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSetupManagementExplicitLocalPassesThrough(t *testing.T) {
	ui := &fakeSetupUI{tty: true}

	management, err := resolveSetupManagement(ui, tunnelManagementLocal, "")
	if err != nil {
		t.Fatalf("resolveSetupManagement: %v", err)
	}
	if management != tunnelManagementLocal {
		t.Fatalf("management = %q, want %q", management, tunnelManagementLocal)
	}
	if len(ui.selectPrompts) != 0 {
		t.Fatalf("select prompts = %d, want 0", len(ui.selectPrompts))
	}
}

func TestResolveRemoteSetupOptionsWrapsLookupFailure(t *testing.T) {
	ui := &fakeSetupUI{}
	client := &fakeSetupCloudflareClient{zoneErr: errors.New("boom")}

	_, _, err := resolveRemoteSetupOptions(ui, client, "stack.example.com", SetupOptions{
		Hostname: "stack.example.com",
	}, func(DomainRegisterOptions) (*DomainRegisterResult, error) {
		t.Fatal("registerDomain should not be called")
		return nil, nil
	}, func(string) (*cloudflareZone, error) {
		t.Fatal("waitForZone should not be called")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected lookup error")
	}
	if !strings.Contains(err.Error(), "cloudflare zone lookup failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveRemoteSetupOptionsPropagatesWaitForZoneError(t *testing.T) {
	ui := &fakeSetupUI{tty: true}
	client := &fakeSetupCloudflareClient{zoneErr: errCloudflareZoneNotFound}

	_, _, err := resolveRemoteSetupOptions(ui, client, "stack.example.com", SetupOptions{
		Hostname:       "stack.example.com",
		RegisterDomain: true,
		AccountID:      "acct-123",
	}, func(opts DomainRegisterOptions) (*DomainRegisterResult, error) {
		return &DomainRegisterResult{AccountID: opts.AccountID}, nil
	}, func(string) (*cloudflareZone, error) {
		return nil, errors.New("zone still provisioning")
	})
	if err == nil {
		t.Fatal("expected wait-for-zone error")
	}
	if !strings.Contains(err.Error(), "zone is not ready yet") {
		t.Fatalf("unexpected error: %v", err)
	}
}
