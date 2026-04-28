# ForkObolToken parity vs canonical OBOL

`ForkObolToken.sol` is a deliberately minimal ERC-20 + ERC-2612 (`permit`) implementation used by the live Base Sepolia OBOL flow (`flows/flow-14-live-obol-base-sepolia.sh`) when an official OBOL deployment is not available on the target chain. It does **not** try to be drop-in compatible with the canonical OBOL token in every way — it only asserts the bits that affect x402 Permit2 signing and settlement.

This document records the parity guarantees and the deltas, so reviewers (and future contributors) can audit each one independently.

## Canonical reference

- Address (Ethereum mainnet): [`0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7`](https://etherscan.io/address/0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7)
- Source (verified): [Sourcify full-match](https://repo.sourcify.dev/contracts/full_match/1/0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7/sources/src/ObolToken.sol) — `src/ObolToken.sol`
- Compiler: `solc 0.8.17`, optimizer enabled, 200 runs, EVM `london`
- Inheritance: OpenZeppelin `ERC20` + `ERC20Permit` + `ERC20Votes` + `AccessControl`

## What MUST match (and is asserted in tests)

The test `internal/testutil/forkobol_parity_test.go::TestForkObolToken_ParityWithCanonicalOBOL` enforces these at every `go test ./...` run.

| Invariant | Canonical value | Why it matters |
|---|---|---|
| EIP-712 domain typehash | `keccak256("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")` = `0x8b73c3c69bb8fe3d512ecc4cf759cc79239f7b179b0ffacaa9a75d522b39400f` | The buyer signs `\x19\x01 ‖ domainSeparator ‖ structHash`. Drift here invalidates every signature. |
| Permit struct typehash | `keccak256("Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)")` = `0x6e71edae12b1b97f4d1f60370fef10105fa2faae0126114a169c64845d6126c9` | The `structHash` the buyer signs is `keccak256(abi.encode(typehash, owner, spender, value, nonce, deadline))`. Same drift consequence. |
| Name hash | `keccak256("Obol Network")` = `0xc272cbc85e9267f7a7104c8745c6b9edcd2dcf6627beaed25edd4cf95159d5fc` | Goes into the EIP-712 domain. The ServiceOffer's `eip712Name` must equal this string verbatim. |
| Version hash | `keccak256("1")` = `0xc89efdaa54c0f20c7adf612882df0950f5a951637e0307cdcb4c672f298b8bc6` | Goes into the EIP-712 domain. The ServiceOffer's `eip712Version` must equal `"1"` verbatim. |
| `decimals` | `18` | The buyer multiplies the human-readable price by `10^decimals` before signing. Drift here means buyer signs the wrong amount. |
| Domain separator formula | `keccak256(abi.encode(typeHash, nameHash, versionHash, block.chainid, address(this)))` | This formula, applied to mainnet (`chainid=1`, address `0x0B01…`), reproduces the exact `DOMAIN_SEPARATOR()` returned by a live `cast call`: `0x5a3cd81e467dcdfe5d4ed4383d31f23bd6ce41b7be43812c5554ba9f7d949432`. |

The parity test independently:

1. Greps the `.sol` source for the keccak256 string literals (catches accidental constant edits).
2. Computes their keccak256 in Go and compares to the canonical bytes (catches typo drift).
3. Reproduces the mainnet domain separator from the formula (catches encoding drift).

## What is intentionally different

These deltas are listed so reviewers don't waste time spotting them as bugs.

| Feature | ForkObolToken | Canonical OBOL | Reason |
|---|---|---|---|
| Voting / checkpoints | absent | `ERC20Votes` (governance) | Not used by x402 Permit2; keeps the test contract small. |
| Minter access control | unrestricted `mint()` | `MINTER_ROLE`-gated | Test convenience. The contract is only deployed on testnet and a labelled fork. |
| ENS reverse registrar | absent | sets `obol.eth` reverse record | Cosmetic. |
| `burn` / `burnFrom` | absent | present | Not used by Permit2 settlement. |
| Transfer-to-self block | absent | `_to != address(this)` | Defensive only. |
| Domain separator caching | caches `_initialChainId` + `_initialDomainSeparator`, recomputes on chainId change | OpenZeppelin caches the same plus `_CACHED_THIS` | OZ caches `address(this)` to handle proxy delegatecall. ForkObolToken is not proxiable, so caching `address(this)` is unnecessary. |

## Verification recipe

For an external auditor wanting to reproduce the parity claim from scratch:

```sh
# 1. Confirm the canonical contract on mainnet still returns the expected bytes:
cast call 0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7 \
  'DOMAIN_SEPARATOR()(bytes32)' \
  --rpc-url https://ethereum-rpc.publicnode.com
# expect: 0x5a3cd81e467dcdfe5d4ed4383d31f23bd6ce41b7be43812c5554ba9f7d949432

# 2. Run the in-tree parity test:
go test ./internal/testutil/ -run TestForkObolToken_ParityWithCanonicalOBOL -v

# 3. After deploying to your target chain (here, Base Sepolia):
cast call <YOUR_FORK_OBOL_ADDR> 'DOMAIN_SEPARATOR()(bytes32)' \
  --rpc-url https://sepolia.base.org
# Plug into the formula:
#   keccak256(abi.encode(0x8b73c3..., 0xc272cb..., 0xc89efd..., chainid=84532, YOUR_ADDR))
# and confirm equality.
```
