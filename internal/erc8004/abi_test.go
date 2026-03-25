package erc8004

import (
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

func TestABI_ParsesSuccessfully(t *testing.T) {
	_, err := abi.JSON(strings.NewReader(identityRegistryABI))
	if err != nil {
		t.Fatalf("embedded ABI failed to parse: %v", err)
	}
}

func TestABI_AllFunctionsPresent(t *testing.T) {
	parsed, err := parseABI()
	if err != nil {
		t.Fatal(err)
	}

	// The ABI has 10 function entries but go-ethereum deduplicates overloaded
	// names by appending a disambiguator (register, register0, register1).
	// We check for the 7 unique method names plus the overload variants.
	wantMethods := []string{
		"register",  // overload with 1 input (string)
		"register0", // overload with 0 inputs
		"register1", // overload with 2 inputs (string, tuple[])
		"setAgentURI",
		"setMetadata",
		"getMetadata",
		"getAgentWallet",
		"setAgentWallet",
		"unsetAgentWallet",
		"tokenURI",
	}

	for _, name := range wantMethods {
		if _, ok := parsed.Methods[name]; !ok {
			t.Errorf("missing method %q in parsed ABI (have: %s)", name, methodNames(parsed))
		}
	}
}

func TestABI_AllEventsPresent(t *testing.T) {
	parsed, err := parseABI()
	if err != nil {
		t.Fatal(err)
	}

	wantEvents := []string{"Registered", "URIUpdated", "MetadataSet"}
	for _, name := range wantEvents {
		if _, ok := parsed.Events[name]; !ok {
			t.Errorf("missing event %q in parsed ABI", name)
		}
	}
}

func TestABI_RegisterOverloads(t *testing.T) {
	parsed, err := parseABI()
	if err != nil {
		t.Fatal(err)
	}

	// go-ethereum names overloads: register (first seen), register0, register1.
	// The order depends on the ABI JSON order. We identify by input count.
	tests := []struct {
		name       string
		wantInputs int
	}{
		// First in JSON: register(string agentURI) → 1 input
		{"register", 1},
		// Second in JSON: register() → 0 inputs
		{"register0", 0},
		// Third in JSON: register(string, tuple[]) → 2 inputs
		{"register1", 2},
	}

	for _, tt := range tests {
		m, ok := parsed.Methods[tt.name]
		if !ok {
			t.Errorf("missing method %q", tt.name)
			continue
		}

		if len(m.Inputs) != tt.wantInputs {
			t.Errorf("method %q: got %d inputs, want %d", tt.name, len(m.Inputs), tt.wantInputs)
		}
	}
}

func TestConstants_Addresses(t *testing.T) {
	addrs := []struct {
		name string
		addr string
	}{
		{"IdentityRegistryBaseSepolia", IdentityRegistryBaseSepolia},
		{"ReputationRegistryBaseSepolia", ReputationRegistryBaseSepolia},
		{"ValidationRegistryBaseSepolia", ValidationRegistryBaseSepolia},
	}

	for _, a := range addrs {
		t.Run(a.name, func(t *testing.T) {
			if !strings.HasPrefix(a.addr, "0x") {
				t.Fatalf("address %q does not start with 0x", a.addr)
			}

			hex := a.addr[2:]
			if len(hex) != 40 {
				t.Errorf("address hex part is %d chars, want 40", len(hex))
			}

			for _, c := range hex {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
					t.Errorf("address contains non-hex char %q", string(c))
					break
				}
			}
		})
	}
}

// methodNames returns a comma-separated list of method names for diagnostics.
func methodNames(a abi.ABI) string {
	names := make([]string, 0, len(a.Methods))
	for n := range a.Methods {
		names = append(names, n)
	}

	return strings.Join(names, ", ")
}
