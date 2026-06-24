package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// TestDomainAPITokenHasNoShortAlias guards the credential-collision fix: the
// Cloudflare API token flag must NOT reuse `-t`, which `obol tunnel setup` uses
// for the (different) connector token. A regression here would let a user paste
// one credential where the other is expected.
func TestDomainAPITokenHasNoShortAlias(t *testing.T) {
	var sawAPIToken, sawAccountID bool
	for _, flag := range domainAuthFlags() {
		names := flag.Names()
		switch names[0] {
		case "api-token":
			sawAPIToken = true
			if slices.Contains(names, "t") {
				t.Fatalf("--api-token must not have a -t alias (collides with tunnel connector token); names=%v", names)
			}
		case "account-id":
			sawAccountID = true
			if !slices.Contains(names, "a") {
				t.Fatalf("--account-id should keep its -a alias; names=%v", names)
			}
		}
	}
	if !sawAPIToken || !sawAccountID {
		t.Fatalf("domainAuthFlags missing expected flags (api-token=%v account-id=%v)", sawAPIToken, sawAccountID)
	}
}

func TestResolveDomainAPITokenReturnsSupplied(t *testing.T) {
	got, err := resolveDomainAPIToken(ui.New(false), "  cf-token-123  ")
	if err != nil {
		t.Fatalf("resolveDomainAPIToken: %v", err)
	}
	if got != "cf-token-123" {
		t.Fatalf("token = %q, want trimmed cf-token-123", got)
	}
}

// TestResolveDomainAPITokenNonInteractive verifies the non-TTY path returns an
// actionable error (scope + token-creation URL) instead of silently prompting.
func TestResolveDomainAPITokenNonInteractive(t *testing.T) {
	_, err := resolveDomainAPIToken(ui.New(false), "")
	if err == nil {
		t.Fatal("expected an error when no token is supplied in a non-interactive session")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Cloudflare API token") || !strings.Contains(msg, "api-tokens") {
		t.Fatalf("error should name the credential and the token-creation URL, got %q", msg)
	}
}
