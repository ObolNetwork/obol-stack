# x402 + ERC-8004 Integration Test Plan

**Feature branch:** `feat/secure-enclave-inference`
**Scope:** 100% coverage of x402 payment gating, ERC-8004 on-chain registration, verifier service, and CLI commands.

---

## 1. Coverage Inventory

### Current State

| Package | File | Existing Tests | Coverage |
|---------|------|---------------|----------|
| `internal/erc8004` | `client.go` | TestNewClient, TestRegister | ~60% (missing SetAgentURI, SetMetadata error paths) |
| `internal/erc8004` | `store.go` | TestStore | ~70% (missing Save errors, corrupt file) |
| `internal/erc8004` | `types.go` | none | 0% (JSON marshaling/unmarshaling) |
| `internal/erc8004` | `abi.go` | implicit via client tests | ~50% (missing ABI parse error, constant verification) |
| `internal/x402` | `verifier.go` | 11 tests | ~85% (missing SetRegistration, HandleWellKnown) |
| `internal/x402` | `matcher.go` | 8 tests | ~95% (good) |
| `internal/x402` | `config.go` | implicit via verifier | ~40% (missing LoadConfig, ResolveChain edge cases) |
| `internal/x402` | `watcher.go` | none | 0% |
| `internal/x402` | `setup.go` | none | 0% (kubectl-dependent, needs mock) |
| `cmd/obol` | `x402.go` | none | 0% |

### Target: 100% Function Coverage

---

## 2. Unit Tests to Add

### 2.1 `internal/erc8004` Package

#### `abi_test.go` (NEW)

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestABI_ParsesSuccessfully` | Embedded ABI JSON parses without error | HIGH |
| `TestABI_AllFunctionsPresent` | All 10 functions present: register (3 overloads), setAgentURI, setMetadata, getMetadata, getAgentWallet, setAgentWallet, unsetAgentWallet, tokenURI | HIGH |
| `TestABI_AllEventsPresent` | All 3 events present: Registered, URIUpdated, MetadataSet | HIGH |
| `TestABI_RegisterOverloads` | 3 distinct register methods exist with correct input counts (0, 1, 2) | HIGH |
| `TestConstants_Addresses` | IdentityRegistryBaseSepolia, ReputationRegistryBaseSepolia, ValidationRegistryBaseSepolia are valid hex addresses (40 chars after 0x) | MEDIUM |
| `TestConstants_ChainID` | BaseSepoliaChainID == 84532 | LOW |

#### `types_test.go` (NEW)

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestAgentRegistration_MarshalJSON` | Full struct serializes to spec-compliant JSON (type, name, description, image, services, x402Support, active, registrations, supportedTrust) | HIGH |
| `TestAgentRegistration_UnmarshalJSON` | Canonical spec JSON (from ERC8004SPEC.md) deserializes correctly | HIGH |
| `TestAgentRegistration_OmitEmptyFields` | Optional fields (description, image, registrations, supportedTrust) omitted when zero-value | MEDIUM |
| `TestServiceDef_VersionOptional` | ServiceDef without version marshals correctly (version omitempty) | MEDIUM |
| `TestOnChainReg_AgentIDNumeric` | AgentID is int64, serializes as JSON number (not string) | HIGH |
| `TestRegistrationType_Constant` | RegistrationType == `"https://eips.ethereum.org/EIPS/eip-8004#registration-v1"` | LOW |

#### `client_test.go` (ADDITIONS to existing)

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestNewClient_DialError` | Returns error when RPC URL is unreachable | MEDIUM |
| `TestNewClient_ChainIDError` | Returns error when eth_chainId fails | MEDIUM |
| `TestSetAgentURI` | Successful tx + wait mined (mock sendRawTransaction + receipt) | HIGH |
| `TestSetMetadata` | Successful tx + wait mined | HIGH |
| `TestRegister_NoRegisteredEvent` | Returns error when receipt has no Registered event log | HIGH |
| `TestRegister_TxError` | Returns error when sendRawTransaction fails | MEDIUM |
| `TestGetMetadata_EmptyResult` | Returns nil when contract returns empty bytes | MEDIUM |

#### `store_test.go` (ADDITIONS to existing)

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestStore_SaveOverwrite` | Second Save overwrites first | MEDIUM |
| `TestStore_LoadCorruptJSON` | Returns error on malformed JSON file | MEDIUM |
| `TestStore_SaveReadOnly` | Returns error when directory is read-only (permission denied) | LOW |

### 2.2 `internal/x402` Package

#### `verifier_test.go` (ADDITIONS)

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestVerifier_SetRegistration` | SetRegistration stores data, HandleWellKnown returns it | HIGH |
| `TestVerifier_HandleWellKnown_NoRegistration` | Returns 404 when no registration set | HIGH |
| `TestVerifier_HandleWellKnown_JSON` | Response is valid JSON AgentRegistration with correct Content-Type | HIGH |
| `TestVerifier_ReadyzNotReady` | Returns 503 when config is nil (fresh Verifier without config) | MEDIUM |

#### `config_test.go` (NEW)

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestLoadConfig_ValidYAML` | Parses complete YAML with wallet, chain, routes | HIGH |
| `TestLoadConfig_Defaults` | Empty chain defaults to "base-sepolia", empty facilitatorURL defaults | HIGH |
| `TestLoadConfig_InvalidYAML` | Returns parse error on malformed YAML | MEDIUM |
| `TestLoadConfig_FileNotFound` | Returns read error | MEDIUM |
| `TestResolveChain_AllSupported` | All 6 chain names resolve (base, base-sepolia, polygon, polygon-amoy, avalanche, avalanche-fuji) | HIGH |
| `TestResolveChain_Aliases` | "base-mainnet" == "base", "polygon-mainnet" == "polygon", etc. | MEDIUM |
| `TestResolveChain_Unsupported` | Returns error for unknown chain name | MEDIUM |
| `TestResolveChain_ErrorMessage` | Error message lists all supported chains | LOW |

#### `watcher_test.go` (NEW)

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestWatchConfig_DetectsChange` | Write new config file, watcher reloads verifier within interval | HIGH |
| `TestWatchConfig_IgnoresUnchanged` | Same mtime = no reload | MEDIUM |
| `TestWatchConfig_InvalidConfig` | Bad YAML doesn't crash watcher, verifier keeps old config | HIGH |
| `TestWatchConfig_CancelContext` | Context cancellation stops the watcher goroutine cleanly | MEDIUM |
| `TestWatchConfig_MissingFile` | Missing file logged but watcher continues | MEDIUM |

#### `setup_test.go` (NEW — requires abstraction for kubectl)

The `setup.go` file shells out to `kubectl`. To unit-test it, extract an interface:

```go
// KubectlRunner abstracts kubectl execution for testing.
type KubectlRunner interface {
    Run(args ...string) error
    Output(args ...string) (string, error)
}
```

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestSetup_PatchesSecretAndConfigMap` | Calls kubectl patch on both secret and configmap with correct args | HIGH |
| `TestSetup_NoKubeconfig` | Returns "cluster not running" error | HIGH |
| `TestAddRoute_AppendsToExisting` | Reads existing config, appends route, patches back | HIGH |
| `TestAddRoute_FirstRoute` | Adds route when routes list is empty | MEDIUM |
| `TestGetPricingConfig_EmptyResponse` | Returns empty PricingConfig when configmap has no data | MEDIUM |
| `TestGetPricingConfig_ParsesYAML` | Correct wallet/chain/routes from kubectl output | HIGH |
| `TestPatchPricingConfig_Serialization` | Generated YAML has correct format (routes array, descriptions) | MEDIUM |

---

## 3. Integration Tests (//go:build integration)

These require a running k3d cluster with `OBOL_DEVELOPMENT=true`.

### 3.1 `internal/x402/integration_test.go` (NEW)

**Prerequisites:** Running cluster, x402 namespace deployed.

| Test | What it verifies | Runtime | Priority |
|------|-----------------|---------|----------|
| `TestIntegration_X402Setup` | `obol x402 setup --wallet 0x... --chain base-sepolia` patches configmap + secret in cluster | 30s | HIGH |
| `TestIntegration_X402Status` | `obol x402 status` reads correct config from cluster | 15s | HIGH |
| `TestIntegration_X402AddRoute` | `obol x402 setup` then AddRoute() adds route, verifiable via GetPricingConfig | 30s | MEDIUM |
| `TestIntegration_VerifierDeployment` | x402-verifier pod is running, responds to /healthz | 15s | HIGH |
| `TestIntegration_VerifierForwardAuth` | Send request to /verify endpoint with X-Forwarded-Uri, verify 200/402 behavior | 30s | HIGH |
| `TestIntegration_WellKnownEndpoint` | GET /.well-known/agent-registration.json returns valid JSON (after registration set) | 15s | MEDIUM |

### 3.2 `internal/erc8004/integration_test.go` (NEW)

**Prerequisites:** Base Sepolia RPC access, funded test wallet (ERC8004_PRIVATE_KEY env var).

| Test | What it verifies | Runtime | Priority |
|------|-----------------|---------|----------|
| `TestIntegration_RegisterOnBaseSepolia` | Full register() tx on testnet, verify agentID returned | 60s | HIGH |
| `TestIntegration_SetAgentURI` | setAgentURI() after register, verify tokenURI() returns new URI | 60s | HIGH |
| `TestIntegration_SetAndGetMetadata` | setMetadata() + getMetadata() roundtrip | 60s | MEDIUM |
| `TestIntegration_GetAgentWallet` | getAgentWallet() returns owner address after registration | 30s | MEDIUM |

**Skip logic:**
```go
func TestMain(m *testing.M) {
    if os.Getenv("ERC8004_PRIVATE_KEY") == "" {
        fmt.Println("Skipping ERC-8004 integration tests: ERC8004_PRIVATE_KEY not set")
        os.Exit(0)
    }
    os.Exit(m.Run())
}
```

### 3.3 End-to-End: x402 Payment Flow

**File:** `internal/x402/e2e_test.go` (NEW, `//go:build integration`)

**Prerequisites:** Running cluster with inference network deployed, x402 enabled, funded test wallet.

| Test | Scenario | Steps | Priority |
|------|----------|-------|----------|
| `TestE2E_InferenceWithPayment` | Full x402 payment lifecycle | 1. Deploy inference network with x402Enabled=true; 2. Configure pricing via AddRoute; 3. Send request WITHOUT payment → 402; 4. Verify 402 body contains payment requirements; 5. Send request WITH valid x402 payment header → 200 | HIGH |
| `TestE2E_RegisterAndServeWellKnown` | ERC-8004 + well-known endpoint | 1. Register agent on Base Sepolia; 2. Set registration on verifier; 3. GET /.well-known/agent-registration.json → matches registration | MEDIUM |

---

## 4. CLI Command Tests

### `cmd/obol/x402_test.go` (NEW)

Pattern: Build the CLI app, run subcommands against mocked infrastructure.

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestX402Command_Structure` | x402 has 3 subcommands: register, setup, status | HIGH |
| `TestX402Register_RequiresPrivateKey` | Fails without --private-key or ERC8004_PRIVATE_KEY | HIGH |
| `TestX402Register_TrimsHexPrefix` | 0x-prefixed key handled correctly | MEDIUM |
| `TestX402Setup_RequiresWallet` | Fails without --wallet flag | HIGH |
| `TestX402Setup_DefaultChain` | Default chain is "base-sepolia" | MEDIUM |
| `TestX402Status_NoCluster` | Graceful output when no cluster running | MEDIUM |
| `TestX402Status_NoRegistration` | Shows "not registered" message | MEDIUM |

---

## 5. Helmfile Template Tests

### Infrastructure Helmfile (conditional x402 resources)

**File:** `internal/embed/infrastructure/helmfile_test.go` (NEW)

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestHelmfile_X402DisabledByDefault` | x402Enabled=false: no Middleware CRD rendered, no ExtensionRef on eRPC HTTPRoute | HIGH |
| `TestHelmfile_X402Enabled` | x402Enabled=true: Middleware CRD rendered with correct ForwardAuth address, ExtensionRef added to eRPC HTTPRoute | HIGH |

### Inference Network Template

**File:** `internal/embed/networks/inference/template_test.go` (NEW)

| Test | What it verifies | Priority |
|------|-----------------|----------|
| `TestInferenceValues_X402EnabledField` | values.yaml.gotmpl contains x402Enabled field with @enum true,false, @default false | HIGH |
| `TestInferenceHelmfile_X402Passthrough` | x402Enabled value passed through to helmfile.yaml.gotmpl | HIGH |
| `TestInferenceGateway_ConditionalMiddleware` | gateway.yaml: Middleware CRD only rendered when x402Enabled=true | HIGH |
| `TestInferenceGateway_ConditionalExtensionRef` | gateway.yaml: ExtensionRef only present when x402Enabled=true | HIGH |

---

## 6. Coverage Gap Analysis — Functions NOT Tested

### internal/erc8004

| Function | File:Line | Test Status | Action |
|----------|-----------|-------------|--------|
| `NewClient()` | client.go:26 | TESTED | - |
| `Close()` | client.go:57 | implicit | - |
| `Register()` | client.go:63 | TESTED | Add error paths |
| `SetAgentURI()` | client.go:95 | **UNTESTED** | Add test |
| `SetMetadata()` | client.go:114 | **UNTESTED** | Add test |
| `GetMetadata()` | client.go:133 | TESTED | Add empty result |
| `TokenURI()` | client.go:150 | TESTED | - |
| `NewStore()` | store.go:30 | implicit | - |
| `Save()` | store.go:39 | TESTED | Add error paths |
| `Load()` | store.go:55 | TESTED | Add corrupt file |

### internal/x402

| Function | File:Line | Test Status | Action |
|----------|-----------|-------------|--------|
| `NewVerifier()` | verifier.go:25 | TESTED | - |
| `Reload()` | verifier.go:34 | TESTED | - |
| `HandleVerify()` | verifier.go:56 | TESTED (11 cases) | - |
| `HandleHealthz()` | verifier.go:114 | TESTED | - |
| `HandleReadyz()` | verifier.go:120 | TESTED | Add nil config case |
| `SetRegistration()` | verifier.go:131 | **UNTESTED** | Add test |
| `HandleWellKnown()` | verifier.go:136 | **UNTESTED** | Add test |
| `LoadConfig()` | config.go:46 | **UNTESTED** | Add tests |
| `ResolveChain()` | config.go:69 | partial (error case only) | Add all chains |
| `WatchConfig()` | watcher.go:16 | **UNTESTED** | Add tests |
| `Setup()` | setup.go:23 | **UNTESTED** | Needs kubectl abstraction |
| `AddRoute()` | setup.go:70 | **UNTESTED** | Needs kubectl abstraction |
| `GetPricingConfig()` | setup.go:96 | **UNTESTED** | Needs kubectl abstraction |
| `matchRoute()` | matcher.go:19 | TESTED (8 cases) | - |
| `matchPattern()` | matcher.go:29 | TESTED | - |
| `globMatch()` | matcher.go:52 | TESTED | - |

---

## 7. Implementation Priority

### Phase 1: Unit tests (no cluster needed) — ~2 hours

1. `internal/erc8004/abi_test.go` — ABI integrity checks
2. `internal/erc8004/types_test.go` — JSON serialization spec compliance
3. `internal/x402/config_test.go` — LoadConfig + ResolveChain
4. `internal/x402/verifier_test.go` — SetRegistration + HandleWellKnown additions
5. `internal/x402/watcher_test.go` — File watcher

### Phase 2: Missing client methods + error paths — ~1 hour

6. `internal/erc8004/client_test.go` — SetAgentURI, SetMetadata, error paths
7. `internal/erc8004/store_test.go` — overwrite, corrupt, permissions

### Phase 3: Setup abstraction + tests — ~1.5 hours

8. Extract `KubectlRunner` interface from `setup.go`
9. `internal/x402/setup_test.go` — all Setup/AddRoute/GetPricingConfig

### Phase 4: Integration tests — ~2 hours (requires running cluster)

10. `internal/x402/integration_test.go` — cluster-based tests
11. `internal/erc8004/integration_test.go` — Base Sepolia testnet tests

### Phase 5: Template + CLI tests — ~1 hour

12. Helmfile template rendering tests
13. `cmd/obol/x402_test.go` — CLI command structure + validation

---

## 8. Test Execution Commands

```bash
# Phase 1-3: Unit tests only
go test -v ./internal/erc8004/... ./internal/x402/...

# Phase 4: Integration tests (requires cluster + testnet key)
export OBOL_CONFIG_DIR=$(pwd)/.workspace/config
export OBOL_BIN_DIR=$(pwd)/.workspace/bin
export OBOL_DATA_DIR=$(pwd)/.workspace/data
export ERC8004_PRIVATE_KEY=<base-sepolia-funded-key>
go build -o .workspace/bin/obol ./cmd/obol
go test -tags integration -v -timeout 15m ./internal/x402/ ./internal/erc8004/

# Coverage report
go test -coverprofile=coverage.out ./internal/erc8004/... ./internal/x402/...
go tool cover -html=coverage.out -o coverage.html
```

---

## 9. Success Criteria

- [ ] 100% function coverage on `internal/erc8004/` (all 10 exported functions)
- [ ] 100% function coverage on `internal/x402/` (all 14 exported functions)
- [ ] All 3 ABI register overloads verified against canonical ABI
- [ ] JSON serialization roundtrip matches ERC-8004 spec format
- [ ] WatchConfig tested with file changes, cancellation, and error recovery
- [ ] Setup/AddRoute/GetPricingConfig tested via kubectl mock
- [ ] HandleWellKnown tested (200 with data, 404 without)
- [ ] Integration tests skip gracefully when prerequisites unavailable
- [ ] `go test ./...` passes with zero failures
