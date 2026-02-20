# Phase 2b — Linux TEE Backend

> Status: **PLANNING**
> Branch: `feat/secure-enclave-inference`

## What We're Building

Phase 2a shipped the Apple Secure Enclave backend (`internal/enclave/enclave_darwin.go`)
and the x402 gateway.  Phase 2b extends the same `enclave.Key` interface to Linux hardware
TEEs — Intel TDX, AMD SEV-SNP, and AWS Nitro Enclaves.  The goal is a gateway that can
run inside a CoCo (Confidential Containers) pod on bare-metal k3s and produce a
hardware-verifiable attestation quote that binds its public key to the model being served.

---

## Threat Model Recap

| Attacker | Mitigated By |
|----------|-------------|
| Network eavesdropper | ECIES per-request encryption (Phase 2a — done) |
| Operator reads inference traffic | TEE memory isolation — private key lives in TVM/SNP VM |
| Model identity spoofing | `modelHash` bound into attestation `user_data` |
| Key leaks across restarts | Persistent key stored in TEE-protected storage or re-attested on each boot |

Phase 2b covers rows 2 and 3.  Row 4 is a Phase 3 concern (on-chain anchoring via ERC-8004).

---

## Architecture

```
                 ┌──────────────────────────────────────────────────┐
                 │  CoCo Pod (kata-runtime / TDX / SNP VM)          │
                 │                                                    │
Client ─ECIES──▶│  internal/tee/key.go   ←── implements enclave.Key│
                 │         │                                          │
                 │         ▼                                          │
                 │  /v1/attestation   (TDX quote + pubkey + hash)   │
                 │  /v1/enclave/pubkey (existing — pubkey JSON)      │
                 │  /v1/chat/completions (x402 + SE decrypt → Ollama)│
                 └──────────────────────────────────────────────────┘
                          │  attestation quote
                          ▼
              Intel TDX TDCALL / AMD SNP GET_REPORT
              → quote contains SHA256(pubkey || modelHash)
```

**Key insight**: The private key is generated *inside* the TEE.  It never exists in host
memory.  The attestation report proves to any verifier that the key is bound to a specific
hardware measurement and a specific model hash — without the operator being able to extract
the key.

---

## Package Layout

```
internal/
├── enclave/                     (existing — macOS SE backend)
│   ├── enclave.go               Key interface (unchanged)
│   ├── enclave_darwin.go        darwin+cgo backend
│   ├── enclave_stub.go          !darwin||!cgo stub
│   └── ecies.go                 ECIES encrypt helper (reused by tee)
│
└── tee/                         (NEW — Linux TEE backend)
    ├── tee.go                   package doc + TEEType enum + exported API
    ├── key.go                   teeKey struct implements enclave.Key
    ├── attest_tdx.go            Intel TDX: TDCALL(TDREPORT) → quote (linux+cgo)
    ├── attest_snp.go            AMD SEV-SNP: /dev/sev-guest ioctl (linux+cgo)
    ├── attest_nitro.go          AWS Nitro: nsm.GetAttestationDocument (linux+cgo)
    ├── attest_stub.go           Software stub for dev + non-linux (all platforms)
    ├── verify.go                Client-side: parse + verify quote offline
    └── tee_test.go              Unit tests (stub backend only; hardware tests separate)
```

### Build Tags

| File | Build tag | Purpose |
|------|-----------|---------|
| `attest_tdx.go` | `linux && cgo && tdx` | TDX TDCALL via CGo |
| `attest_snp.go` | `linux && cgo && snp` | SNP /dev/sev-guest via CGo |
| `attest_nitro.go` | `linux && cgo && nitro` | Nitro NSM via CGo |
| `attest_stub.go` | `!tdx && !snp && !nitro` | Fallback for dev mode + macOS |

Build with `go build -tags tdx ./...` on TDX hardware; normal `go build` everywhere else
uses the stub automatically.

---

## `internal/tee/tee.go` — Public API

```go
// TEEType identifies the hardware TEE backend.
type TEEType string

const (
    TEETypeTDX   TEEType = "tdx"
    TEETypeSNP   TEEType = "snp"
    TEETypeNitro TEEType = "nitro"
    TEETypeStub  TEEType = "stub"  // software fallback, dev mode
)

// AttestationReport is returned by /v1/attestation.
type AttestationReport struct {
    TEEType   TEEType `json:"tee_type"`
    Pubkey    string  `json:"pubkey"`     // hex-encoded 65-byte uncompressed P-256
    ModelHash string  `json:"model_hash"` // hex SHA-256 of model weights/ID
    Quote     []byte  `json:"quote"`      // raw TEE quote bytes (base64 in JSON)
    Timestamp int64   `json:"timestamp"`  // Unix seconds
}

// NewKey generates (or loads) a P-256 key inside the TEE and returns a Key
// handle that satisfies enclave.Key.
//
// tag is an arbitrary string used to namespace the key (same semantics as the
// macOS enclave tag).  modelHash is the SHA-256 of the model being served —
// it is bound into the attestation report user_data so verifiers can confirm
// the model identity.
//
// On stub builds the key is an in-process P-256 key with no hardware backing.
func NewKey(tag, modelHash string) (enclave.Key, error)

// Attest returns the current attestation report for the given key and model.
// Returns ErrNotSupported on stub builds.
func Attest(key enclave.Key, modelHash string) (*AttestationReport, error)
```

---

## `internal/tee/key.go` — Key Implementation

```go
// teeKey satisfies enclave.Key on Linux TEE builds.
// On stub builds it wraps an in-process ecdsa.PrivateKey.
type teeKey struct {
    tag       string
    pubBytes  []byte            // cached 65-byte uncompressed P-256
    modelHash string

    // backend is one of: *tdxBackend | *snpBackend | *nitroBackend | *stubBackend
    backend interface {
        sign(digest []byte) ([]byte, error)
        ecdh(peer []byte) ([]byte, error)
        decrypt(ct []byte) ([]byte, error)
        attest(userData []byte) ([]byte, error) // returns raw quote
        delete() error
    }
}

func (k *teeKey) PublicKeyBytes() []byte                  { return k.pubBytes }
func (k *teeKey) Tag() string                             { return k.tag }
func (k *teeKey) Persistent() bool                        { return true }
func (k *teeKey) Sign(d []byte) ([]byte, error)           { return k.backend.sign(d) }
func (k *teeKey) ECDH(p []byte) ([]byte, error)           { return k.backend.ecdh(p) }
func (k *teeKey) Decrypt(ct []byte) ([]byte, error)       { return k.backend.decrypt(ct) }
func (k *teeKey) Delete() error                           { return k.backend.delete() }
```

The `Decrypt` method reuses `internal/enclave/ecies.go`'s `decrypt()` helper — the ECIES
wire format is identical.  Only the final ECDH step goes to the TEE; AES-GCM is
in-process (same as the macOS backend).

---

## `internal/tee/attest_tdx.go` — TDX Backend

```c
// CGo bridge — calls TDCALL(TDREPORT) to get a TD report,
// then delegates to the TDX Quote Generation Service (QGS) for a full quote.
```

```go
//go:build linux && cgo && tdx

// tdxBackend uses the Intel TDX TDCALL instruction to produce a TD Report,
// then requests a full quote from the Quote Generation Service (QGS) over
// the host-side /dev/tdx_guest device.
type tdxBackend struct {
    privKey *ecdsa.PrivateKey // generated on NewKey, stays in TVM memory
}

// userData = SHA256(pubkeyBytes || modelHashBytes)
// Embedded into TDREPORT.reportData[0:32] (64 bytes available; we use 32).
func (b *tdxBackend) attest(userData []byte) ([]byte, error) {
    // 1. Build TDREPORT via TDCALL leaf 4 (TDG.MR.REPORT)
    // 2. Send TDREPORT to QGS via /dev/tdx_guest ioctl (TDX_CMD_GET_QUOTE)
    // 3. Return raw DCAP quote bytes
}
```

**Dependencies**:
- `github.com/intel/confidential-containers/tee-attestation` (Intel's Go wrapper)
  or CGo direct to `linux/tdx-guest.h` / `ioctl(TDX_CMD_GET_QUOTE)`
- QGS must run on the host or as a side-car; expose via Unix socket
  `/run/tdx-qgs/qgs.socket` or TCP `localhost:4050`

**Key generation**: `ecdsa.GenerateKey(elliptic.P256(), rand.Reader)` inside the TVM.
The private key lives in TVM guest memory; even the host hypervisor cannot read it.

---

## `internal/tee/attest_snp.go` — AMD SEV-SNP Backend

```go
//go:build linux && cgo && snp

// snpBackend uses /dev/sev-guest ioctl SNP_GET_REPORT.
// The 64-byte user_data field carries SHA256(pubkey||modelHash).
type snpBackend struct {
    privKey *ecdsa.PrivateKey
}

func (b *snpBackend) attest(userData []byte) ([]byte, error) {
    // ioctl(fd, SNP_GET_REPORT, &req) where req.UserData = userData[:64]
    // Returns raw SNP AttestationReport (1184 bytes)
}
```

**Dependencies**:
- `github.com/virtee/sev-snp-guest` Go bindings (thin CGo wrapper around the kernel ABI)
- No external service required — the kernel returns the signed report directly from the PSP

---

## `internal/tee/attest_nitro.go` — AWS Nitro Enclave Backend

```go
//go:build linux && cgo && nitro

// nitroBackend uses the AWS Nitro Security Module (NSM) device at /dev/nsm.
// UserData is SHA256(pubkey||modelHash) encoded as CBOR.
type nitroBackend struct {
    privKey *ecdsa.PrivateKey
}

func (b *nitroBackend) attest(userData []byte) ([]byte, error) {
    // nsm.GetAttestationDocument(nonce=nil, userData=userData, publicKey=pubKeyDER)
    // Returns signed CBOR document rooted at Nitro CA
}
```

**Dependencies**:
- `github.com/hf/nsm` — Go bindings for the Nitro NSM device

---

## `internal/tee/attest_stub.go` — Software Stub (Dev Mode)

```go
//go:build !tdx && !snp && !nitro

// stubBackend generates a normal in-process P-256 key.
// attest() returns a dummy "quote" so the gateway starts without hardware.
type stubBackend struct {
    privKey *ecdsa.PrivateKey
}

func (b *stubBackend) attest(userData []byte) ([]byte, error) {
    // Returns a JSON-encoded stub quote for dev use.
    // Real verifiers will reject this; it's for local testing only.
    doc := map[string]any{
        "type":      "stub",
        "user_data": hex.EncodeToString(userData),
        "timestamp": time.Now().Unix(),
    }
    return json.Marshal(doc)
}
```

This ensures `go test ./internal/tee/...` runs on any machine (including macOS CI) without
hardware or build tags.

---

## `internal/tee/verify.go` — Client-Side Verification

```go
// VerifyTDX parses and verifies a TDX DCAP quote against Intel's public PCK certs.
// Returns the parsed TD measurements (MRTD, MRCONFIGID, RTMRs) if valid.
func VerifyTDX(quote []byte, expectedUserData []byte) (*TDXMeasurements, error)

// VerifySNP parses and verifies an AMD SEV-SNP attestation report against
// AMD's VCEK certificate chain.
func VerifySNP(report []byte, expectedUserData []byte) (*SNPMeasurements, error)

// VerifyNitro verifies an AWS Nitro attestation document against the Nitro CA chain.
func VerifyNitro(doc []byte, expectedUserData []byte) (*NitroMeasurements, error)

// ExtractUserData is a convenience helper that extracts user_data from a quote
// regardless of TEE type (detected from the report header).
func ExtractUserData(quote []byte) ([]byte, TEEType, error)
```

**UserData binding contract**:
```
user_data = SHA256(pubkeyBytes || modelHashBytes)
         = SHA256(65-byte uncompressed P-256 || 32-byte SHA-256 of model)
```

Any client can independently verify:
1. `user_data` in the quote equals `SHA256(advertised_pubkey || advertised_model_hash)`
2. The quote signature is valid (Intel/AMD/Amazon PKI)
3. Therefore the gateway is running on real TEE hardware with that exact key and model

---

## Gateway Changes (`internal/inference/gateway.go`)

### New `GatewayConfig` Fields

```go
// TEEType specifies the Linux TEE backend.  When non-empty, the gateway
// uses internal/tee instead of internal/enclave for key management.
// Valid values: "tdx", "snp", "nitro", "stub".
TEEType string

// ModelHash is the hex-encoded SHA-256 of the model being served.
// Required when TEEType is set.  Bound into the TEE attestation user_data.
ModelHash string
```

### New `/v1/attestation` Endpoint

Registered alongside `/v1/enclave/pubkey` when TEE mode is active:

```go
mux.HandleFunc("GET /v1/attestation", func(w http.ResponseWriter, r *http.Request) {
    report, err := tee.Attest(g.teeKey, g.config.ModelHash)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(report)
})
```

Response shape:
```json
{
  "tee_type": "tdx",
  "pubkey": "04a1b2c3...",
  "model_hash": "sha256:abc123...",
  "quote": "<base64-encoded DCAP quote>",
  "timestamp": 1708400000
}
```

### Key Selection Logic in `buildHandler`

```go
// In buildHandler — select backend based on TEEType vs EnclaveTag.
var seKey enclave.Key
switch {
case cfg.TEEType != "":
    // Linux TEE path
    seKey, err = tee.NewKey("com.obol.inference."+deployName, cfg.ModelHash)
case cfg.EnclaveTag != "":
    // macOS Secure Enclave path (existing)
    if err := enclave.CheckSIP(); err != nil { ... }
    seKey, err = enclave.NewKey(cfg.EnclaveTag)
}
```

---

## CLI Changes (`cmd/obol/inference.go`)

### New Flags on `deployFlags()`

```go
&cli.StringFlag{
    Name:  "tee",
    Usage: "linux TEE backend: tdx|snp|nitro|stub",
},
&cli.StringFlag{
    Name:  "model-hash",
    Usage: "sha256 of model weights bound into TEE attestation",
},
```

### Deployment Struct (`internal/inference/store.go`)

```go
type Deployment struct {
    // ... existing fields ...

    // TEEType is the Linux TEE backend ("tdx", "snp", "nitro", "stub").
    // Empty means macOS SE mode.
    TEEType string `json:"tee_type,omitempty"`

    // ModelHash is the hex SHA-256 of the model being served.
    // Required when TEEType is set.
    ModelHash string `json:"model_hash,omitempty"`
}
```

### Usage

```bash
# Dev mode (stub — no real TEE hardware, useful for testing)
obol inference deploy --name local-dev --tee stub \
    --wallet 0xABC \
    --model-hash sha256:$(echo -n "llama3" | sha256sum | awk '{print $1}')

# TDX bare-metal
obol inference deploy --name prod-tdx --tee tdx \
    --wallet 0xABC \
    --model-hash sha256:<weights-hash>

# Query attestation
curl http://localhost:8402/v1/attestation | jq .
```

---

## CoCo Deployment (Bare-Metal k3s)

### Why Bare-Metal k3s (Not k3d)

k3d runs Kubernetes nodes inside Docker containers.  Docker uses a namespace/cgroup
isolation model that is *not* TEE-aware.  CoCo requires:
- Kata Containers runtime with a real hardware TEE shim
- `/dev/tdx_guest` or `/dev/sev-guest` device pass-through
- Physical host CPUs with TDX or SEV-SNP enabled in BIOS

k3d's nested virtualisation would intercept `TDCALL` instructions, making them
non-functional.  Bare-metal k3s + CoCo operator is the only supported path.

### k3s + CoCo Install Flow

```bash
# 1. Install k3s (single-node for dev, multi-node for prod)
curl -sfL https://get.k3s.io | sh -s - \
    --disable traefik \
    --disable servicelb

# 2. Install CoCo operator
kubectl apply -f https://github.com/confidential-containers/operator/releases/latest/download/install.yaml
kubectl apply -f https://github.com/confidential-containers/operator/releases/latest/download/ccruntime-tdx.yaml  # or snp

# 3. Verify kata-tdx runtime class exists
kubectl get runtimeclass | grep kata
```

### Obol Inference Pod Spec

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: obol-inference-<id>
  namespace: inference
spec:
  runtimeClassName: kata-tdx   # or kata-snp / kata-qemu-coco-dev (dev mode)
  containers:
    - name: gateway
      image: ghcr.io/obolnetwork/obol-inference:latest
      args:
        - inference
        - serve
        - --tee=tdx
        - --wallet=$(WALLET_ADDR)
        - --model-hash=$(MODEL_HASH)
      env:
        - name: WALLET_ADDR
          valueFrom:
            secretKeyRef:
              name: obol-inference-<id>
              key: wallet
        - name: MODEL_HASH
          value: "sha256:..."
      ports:
        - containerPort: 8402
    - name: ollama
      image: ollama/ollama:latest
      resources:
        limits:
          nvidia.com/gpu: "1"   # optional
```

The gateway and Ollama run as separate containers in the same pod.  The pod runs inside
the TDX TVM; all intra-pod traffic stays inside the trust boundary.

### obol CLI Integration (Future)

```bash
obol inference k8s deploy --name prod-tdx --tee tdx \
    --wallet 0xABC --model-hash sha256:... --namespace inference
```

This is a Phase 2b+ stretch goal — wire the pod spec generation into the existing
`obol inference deploy` flow for Linux/Kubernetes targets.

---

## ERC-8004 Validation Registry Anchoring

On first startup (after the TEE key is generated and the first attestation is ready),
the gateway calls the Validation Registry to anchor the attestation on-chain:

```go
// internal/erc8004/client.go  (NEW in Phase 3 — referenced here for context)

// AnchorAttestation submits a TEE attestation to the ERC-8004 Validation Registry.
// agentID is the ERC-8004 NFT token ID for this inference node.
// This creates an immutable on-chain record linking a model hash to a TEE quote.
func AnchorAttestation(ctx context.Context, cfg Config, agentID *big.Int, report *tee.AttestationReport) error
```

Contract addresses (Base Sepolia):
- Identity Registry: `0x8004A818BFB912233c491871b3d84c89A494BD9e`
- Validation Registry: `0x...` (look up from `erc-8004-contracts` repo)

This is scoped to Phase 3.  Phase 2b produces the attestation report; Phase 3 anchors it.

---

## Testing Strategy

### Tier 1: Unit Tests (Stub — All Platforms)

```bash
go test ./internal/tee/...
```

Tests run with the stub backend on any machine.  Cover:
- Key generation + `PublicKeyBytes()` shape (65 bytes, 0x04 prefix)
- ECIES round-trip: encrypt with `enclave.Encrypt(pubkey, plain)`, decrypt via `teeKey.Decrypt(ct)`
- `Attest()` stub returns valid JSON with `user_data` matching `SHA256(pubkey||modelHash)`
- `verify.ExtractUserData()` round-trips the stub quote
- `GatewayConfig.TEEType = "stub"` starts gateway, `/v1/attestation` returns 200

### Tier 2: Integration Tests (QEMU Dev Mode — No Hardware Required)

CoCo ships `kata-qemu-coco-dev` runtime class — a software-emulated confidential VM.
It doesn't produce verifiable quotes but lets you test the full pod lifecycle:

```bash
# Requires: k3s + CoCo operator installed, kata-qemu-coco-dev runtime class
go test -tags integration -v ./internal/tee/ -run TestTEEQEMU
```

### Tier 3: Hardware Tests (TDX / SNP / Nitro)

```bash
# On real TDX host with QGS running:
go test -tags tdx,integration -v ./internal/tee/ -run TestTDXAttestation

# On real SNP host:
go test -tags snp,integration -v ./internal/tee/ -run TestSNPAttestation
```

These are CI gated on hardware labels and are not required for PR merges.

---

## File Checklist

| File | Status | Notes |
|------|--------|-------|
| `internal/tee/tee.go` | TODO | Package doc, `TEEType`, `AttestationReport`, exported `NewKey`, `Attest` |
| `internal/tee/key.go` | TODO | `teeKey` struct + `enclave.Key` interface impl |
| `internal/tee/attest_stub.go` | TODO | Dev/non-linux fallback |
| `internal/tee/attest_tdx.go` | TODO | `linux && cgo && tdx` |
| `internal/tee/attest_snp.go` | TODO | `linux && cgo && snp` |
| `internal/tee/attest_nitro.go` | TODO | `linux && cgo && nitro` |
| `internal/tee/verify.go` | TODO | Client-side quote parsing + verification |
| `internal/tee/tee_test.go` | TODO | Unit tests (stub only) |
| `internal/inference/gateway.go` | TODO | Add `TEEType`, `ModelHash` fields + `/v1/attestation` endpoint |
| `internal/inference/store.go` | TODO | Add `TEEType`, `ModelHash` to `Deployment` |
| `cmd/obol/inference.go` | TODO | Add `--tee`, `--model-hash` flags |
| `internal/embed/inference/pod.yaml.gotmpl` | TODO | CoCo pod template |
| `go.mod` | TODO | Add tee attestation deps (tdx/snp/nsm) behind build tags |

---

## Sequencing

1. **`internal/tee/` scaffold** — stub + key.go + tee.go + verify.go skeleton.
   Unit tests pass on macOS. No hardware required.  (~2 days)

2. **Gateway wiring** — `TEEType`/`ModelHash` fields, key selection logic, `/v1/attestation`.
   `go test ./internal/inference/` passes with stub.  (~1 day)

3. **CLI flags** — `--tee`, `--model-hash` on `deploy` + `serve`.
   `store.go` persists them. `go test ./cmd/obol/...` passes.  (~half day)

4. **TDX backend** — `attest_tdx.go`, CGo bridge, QGS socket.
   Requires access to TDX dev machine. Run `go test -tags tdx,integration`.  (~3 days)

5. **SNP backend** — `attest_snp.go`, sev-snp-guest bindings.  (~2 days)

6. **Nitro backend** — `attest_nitro.go`, hf/nsm bindings.  (~1 day)

7. **verify.go** — client-side DCAP / SNP / Nitro verification with cert chain pinning.  (~2 days)

8. **CoCo pod spec** + bare-metal k3s install guide.  (~1 day)

9. **Integration tests** (QEMU dev mode).  (~1 day)

10. **ERC-8004 anchor hook** (stub only; full impl in Phase 3).  (~half day)

Total: ~13 developer-days of focused Go work (hardware testing excluded).

---

## Dependencies to Add

```
# TDX (linux + cgo + tdx build tag only)
github.com/intel/confidential-containers/tee-attestation  # check latest
# OR direct CGo to linux/tdx-guest.h — no external dep, lower maintenance risk

# SNP (linux + cgo + snp build tag only)
github.com/virtee/sev-snp-guest  # Go bindings for /dev/sev-guest

# Nitro (linux + cgo + nitro build tag only)
github.com/hf/nsm  # AWS Nitro Security Module

# Verify (all platforms — pure Go)
# Intel DCAP: parse ECDSA quote structure manually (no stable Go lib yet)
# SNP: VCEK cert chain verification using crypto/x509
# Nitro: CBOR parsing via github.com/fxamacker/cbor/v2 (already commonly used)
```

All three TEE deps are gated behind their respective build tags so they don't pull in
anything on macOS or default Linux builds.

---

## Open Questions

1. **Key persistence across TEE restarts**: TDX TVM memory is ephemeral — private key is
   lost on pod restart.  Options:
   - Re-generate on each boot (simple; clients re-fetch pubkey)
   - Seal to TEE-encrypted storage (complex; ties key to specific hardware MRTD)
   Recommendation: Re-generate per boot for Phase 2b.  Sealed storage in Phase 3.

2. **QGS availability**: TDX quoting requires a Quote Generation Service running on the
   host.  On Kubernetes this could be a DaemonSet.  For Phase 2b bare-metal, document
   manual QGS install.  A Helm chart for QGS is a Phase 3 deliverable.

3. **Verification service**: Where do clients verify quotes?  Options:
   - Client-side (via `verify.go` SDK)
   - ERC-8004 Validation Registry (Phase 3)
   - Hosted PCCS (Intel's caching service)
   Phase 2b ships the client-side SDK.  On-chain anchoring is Phase 3.

4. **MRTD pinning**: Production callers will want to pin the expected TD Measurement
   Register (MRTD = hash of the pod's firmware + kernel + rootfs) so they can reject
   compromised images.  This requires a supply chain pipeline to publish expected MRTD
   values.  Out of scope for Phase 2b.
