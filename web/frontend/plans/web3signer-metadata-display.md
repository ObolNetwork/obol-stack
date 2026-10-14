# Frontend: Web3Signer Metadata Display

> **Date**: 2026-02-20
> **Updated**: 2026-02-20 — integrated into OpenClawInstanceCard; added optional balance via eRPC
> **Depends on**: `obol-stack` PR adding Web3Signer per OpenClaw instance

## Summary

Each OpenClaw instance now has a co-located Web3Signer that provides Ethereum transaction signing. A `web3signer-metadata` ConfigMap is created in each OpenClaw namespace containing the public key(s) and Ethereum address(es) — no private key material is exposed.

The frontend should display these wallet addresses **inline within the existing `OpenClawInstanceCard`** component — not as a separate card or section. This keeps wallet info co-located with the instance it belongs to.

Optionally, if the local eRPC proxy is reachable, the card shows the ETH balance on whichever connected network has funds. If eRPC is unavailable the balance is simply omitted — no errors, no loading states, no layout shift.

## Data Source

**ConfigMap**: `web3signer-metadata` in each `openclaw-*` namespace.

**Discovery**: List ConfigMaps with label `app.kubernetes.io/component=web3signer` across all namespaces. The frontend's existing `ClusterRole` (from `obol-frontend-rbac`) already grants read access to ConfigMaps.

**Key**: `addresses.json` — JSON string with this schema:

```json
{
  "instanceId": "default",
  "addresses": [
    {
      "address": "0xAbCdEf1234567890AbCdEf1234567890AbCdEf12",
      "publicKey": "0x04abcdef...",
      "createdAt": "2026-02-20T14:30:00Z",
      "label": "obol-agent-default"
    }
  ],
  "count": 1
}
```

## API Route — Wallet Metadata

Add a Next.js API route at `src/app/api/openclaw/wallet/route.ts` following the same pattern as the existing `/api/openclaw/status` and `/api/openclaw/token` routes.

**Endpoint**: `GET /api/openclaw/wallet?id={instanceId}`

**Backend logic**: Fetch the `web3signer-metadata` ConfigMap from `openclaw-{id}` namespace, parse `addresses.json`, return the result.

**Response shape**:

```json
{
  "instanceId": "default",
  "addresses": [
    {
      "address": "0xAbCd...Ef12",
      "publicKey": "0x04abcdef...",
      "createdAt": "2026-02-20T14:30:00Z",
      "label": "obol-agent-default"
    }
  ],
  "count": 1
}
```

If the ConfigMap doesn't exist, return `{ "addresses": [], "count": 0 }` (not a 404 — the instance exists, it just doesn't have a signer yet).

## Balance Lookup — via eRPC

### How it works

The balance is a **purely optional decoration**. It's fetched client-side from the local eRPC proxy which is already exposed to the browser at `NEXT_PUBLIC_ERPC_URL` (defaults to `https://obol.stack/rpc`). Each connected network is served at `/{alias}` — e.g., `https://obol.stack/rpc/mainnet`.

The balance hook:

1. Receives a wallet address and fires **after** the address is loaded
2. Fetches the eRPC root status (`GET /`) to discover which networks are healthy (the dashboard already fetches this — see "Optimisation" below)
3. For each healthy network, calls `eth_getBalance` via JSON-RPC, in priority order: mainnet first, then testnets
4. Returns the **first network with a non-zero balance**, along with the network name
5. If all balances are zero, returns the mainnet balance (0 ETH) with network name
6. If eRPC is unreachable, times out, or returns errors — returns `null`. No throw, no error state

### Why client-side, not a new API route

The eRPC proxy URL is already a `NEXT_PUBLIC_` env var intended for browser access (it's the same URL users add to MetaMask). There's no auth, no secrets, and the call is a simple read-only `eth_getBalance`. Adding a server-side route would add latency for no security benefit.

### Resilience contract

The balance must **never** cause problems in the card, regardless of eRPC state:

| eRPC scenario                             | Balance hook returns                       | Card renders                            |
| ----------------------------------------- | ------------------------------------------ | --------------------------------------- |
| eRPC healthy, balance > 0                 | `{ balance: "1.234", network: "mainnet" }` | `0xAbCd...Ef12 [⧉] 1.234 ETH (mainnet)` |
| eRPC healthy, balance = 0 on all networks | `{ balance: "0", network: "mainnet" }`     | `0xAbCd...Ef12 [⧉] 0 ETH (mainnet)`     |
| eRPC unreachable / DNS failure            | `null`                                     | `0xAbCd...Ef12 [⧉]` (no balance shown)  |
| eRPC responds but network unhealthy       | `null` (skips unhealthy networks)          | `0xAbCd...Ef12 [⧉]`                     |
| eRPC times out (>3s)                      | `null` (AbortController fires)             | `0xAbCd...Ef12 [⧉]`                     |
| eRPC returns malformed JSON               | `null` (caught in try/catch)               | `0xAbCd...Ef12 [⧉]`                     |
| Wallet address not yet loaded             | hook doesn't fire                          | No wallet row at all                    |

### Network priority

When the address has balances on multiple connected networks, show **only one**, using this priority:

1. **Mainnet** — always preferred if it has any balance
2. **Testnets** — alphabetical fallback (hoodi, sepolia)

The network name is shown in parentheses after the balance: `1.234 ETH (mainnet)`.

### Optimisation: reuse eRPC status

The dashboard already fetches eRPC status via `/api/dashboard` which returns `erpcStatus.networks` in the `AppData` for the eRPC app. Rather than fetching eRPC status again from the balance hook, we can either:

- **Option A (simple)**: The balance hook fetches `GET {erpcUrl}/` independently. It's a tiny request and cached by eRPC. Adds ~1 extra request per card mount.
- **Option B (optimal)**: Pass the known healthy network aliases into the balance hook as a prop. The `DashboardContent` already has `data.apps` — extract the eRPC networks and thread them through `OpenClawList` → `OpenClawInstanceCard`.

**Recommendation**: Start with Option A. It keeps the hook self-contained and avoids prop-threading through 3 components. Move to Option B if we see unnecessary requests in production.

## Display: Integrated into OpenClawInstanceCard

Wallet info is rendered as additional metadata rows inside the existing `OpenClawInstanceCard` component (`src/components/molecules/OpenClawInstanceCard/OpenClawInstanceCard.tsx`), between the model row and the actions row.

### What to show

| Field          | Source                     | Format                                                   |
| -------------- | -------------------------- | -------------------------------------------------------- |
| Wallet address | `addresses[0].address`     | Truncated `0xAbCd...Ef12` with copy-to-clipboard         |
| Key count      | `count`                    | Only shown if count > 1, as a small badge e.g. "+2 keys" |
| Balance        | `useAddressBalance()` hook | `1.234 ETH (mainnet)` — only if non-null                 |

**Why this subset**: The instance card is compact. The address is the only thing a user needs at a glance. Created time and label are noise here — they could appear in a tooltip or future detail view.

### Conditional rendering

- **No ConfigMap / empty addresses**: Don't render the wallet row at all. The card looks the same as it does today.
- **One address**: Show a single `Wallet: 0xAbCd...Ef12` row with a copy button.
- **Multiple addresses**: Show the first address + a `+N keys` badge. Tooltip or expand to show the rest.
- **Balance available**: Show after the copy button: `1.234 ETH (mainnet)`.
- **Balance unavailable** (eRPC down, timeout, error): Don't show balance. Address + copy button still render fine.
- **Balance loading**: Don't reserve space for it. It fades in when ready (no layout shift — the row is already rendered for the address).
- **Parse error**: Log a console warning. Don't render the wallet row.

### No Signing UI

The frontend must NOT expose any signing, transaction, or key management functionality. It is read-only: display addresses and counts only. All signing is done by the OpenClaw agent via the `ethereum-wallet` skill.

## Multi-Instance Layout (ASCII)

This is how the `OpenClawList` section looks with multiple instances, showing wallet addresses and optional balances inline:

```
┌─────────────────────────────────────────────────────────────────────┐
│  AI Agent Instances                                          [3]   │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  OpenClaw                                       ● Running    │  │
│  │                                                               │  │
│  │  Host:    openclaw-default.obol.stack                         │  │
│  │  Model:   claude-sonnet-4-20250514                                  │  │
│  │  Wallet:  0xAbCd...Ef12  [⧉]  1.234 ETH (mainnet)            │  │
│  │                                                               │  │
│  │  [ Chat ]   Dashboard ↗                                      │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  trading-bot                                    ● Running    │  │
│  │                                                               │  │
│  │  Host:    openclaw-trading-bot.obol.stack                     │  │
│  │  Model:   claude-sonnet-4-20250514                                  │  │
│  │  Wallet:  0x1234...5678  [⧉]  +2 keys   0.5 ETH (hoodi)     │  │
│  │                                                               │  │
│  │  [ Chat ]   Dashboard ↗                                      │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  research                                       ○ Pending    │  │
│  │                                                               │  │
│  │  Host:    openclaw-research.obol.stack                        │  │
│  │  Model:   —                                                   │  │
│  │                                                               │  │
│  │  [ Chat ]   Dashboard ↗                                      │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  monitor                                        ● Running    │  │
│  │                                                               │  │
│  │  Host:    openclaw-monitor.obol.stack                         │  │
│  │  Model:   claude-sonnet-4-20250514                                  │  │
│  │  Wallet:  0x9876...DCBA  [⧉]                                 │  │
│  │                                                               │  │
│  │  [ Chat ]   Dashboard ↗                                      │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

**Key details**:

- `[⧉]` = copy-to-clipboard icon next to the truncated address
- `+2 keys` badge appears only when `count > 1`
- `1.234 ETH (mainnet)` — balance shown with network name in parens; only appears if eRPC responded successfully
- `0.5 ETH (hoodi)` — this instance has funds on a testnet only, so that's what's shown
- `research` instance — no wallet row at all (pending pod, no ConfigMap yet)
- `monitor` instance — has a wallet address but eRPC is down or balance is zero on all networks → no balance shown, address still renders fine
- Wallet row sits between Model and the action buttons, following the existing MetaItem pattern

## Edge Cases

| Case                                      | Behavior                                                    |
| ----------------------------------------- | ----------------------------------------------------------- |
| No `web3signer-metadata` ConfigMap        | Don't render wallet row (instance card otherwise unchanged) |
| ConfigMap exists but `addresses` is empty | Don't render wallet row                                     |
| Single address                            | Show `Wallet: 0xAbCd...Ef12 [copy]`                         |
| Multiple addresses (count > 1)            | Show first address + `+N keys` badge                        |
| ConfigMap JSON parse error                | Log warning, don't render wallet row                        |
| Hook still loading                        | Don't render wallet row (avoids layout shift)               |
| eRPC healthy, balance > 0                 | Show balance + network: `1.234 ETH (mainnet)`               |
| eRPC healthy, balance = 0 all networks    | Show `0 ETH (mainnet)`                                      |
| eRPC unreachable                          | No balance shown; address + copy still work                 |
| eRPC responds but all networks unhealthy  | No balance shown                                            |
| eRPC times out (>3s)                      | No balance shown (AbortController aborts)                   |
| eRPC returns invalid JSON                 | No balance shown (caught, logged)                           |
| Balance on multiple networks              | Show highest-priority non-zero: mainnet > hoodi > sepolia   |

## Data Refresh

The ConfigMap is written once at `obol agent init` time and rarely changes. The wallet metadata hook should fetch once on mount — no polling needed.

The balance hook also fetches once on mount (after the address is available). Balance doesn't need polling — it's informational and the ConfigMap-sourced address is the primary data. If a future requirement needs live balance, add a 60s polling interval.

## Implementation Steps

### 1. Types (`src/types/openclaw.ts`)

Add to the existing file:

```ts
export interface Web3SignerAddress {
  address: string;
  publicKey: string;
  createdAt: string;
  label: string;
}

export interface Web3SignerMetadata {
  instanceId: string;
  addresses: Web3SignerAddress[];
  count: number;
}

export interface AddressBalance {
  balance: string; // formatted ETH string, e.g. "1.234"
  network: string; // network alias, e.g. "mainnet"
}
```

### 2. API Route (`src/app/api/openclaw/wallet/route.ts`)

New route following the same pattern as `src/app/api/openclaw/status/route.ts`:

- Accept `?id=` query param
- Fetch ConfigMap from Kubernetes API: `GET /api/v1/namespaces/openclaw-{id}/configmaps/web3signer-metadata`
- Parse `addresses.json` from the ConfigMap data
- Return the parsed JSON, or `{ addresses: [], count: 0 }` if ConfigMap not found

### 3. Wallet Metadata Hook (`src/hooks/useOpenClaw.ts`)

Add `useWeb3SignerMetadata(instanceId: string)` to the existing hooks file — not a new file. Follows the same pattern as `useOpenClawToken`:

```ts
export function useWeb3SignerMetadata(instanceId: string) {
  const [metadata, setMetadata] = useState<Web3SignerMetadata | null>(null);

  const fetchMetadata = useCallback(async () => {
    try {
      const response = await fetch(`/api/openclaw/wallet?id=${instanceId}`);
      if (response.ok) {
        const data = await response.json();
        setMetadata(data);
      }
    } catch {
      setMetadata(null);
    }
  }, [instanceId]);

  useEffect(() => {
    fetchMetadata();
  }, [fetchMetadata]);

  return { metadata };
}
```

### 4. Balance Hook (`src/hooks/useAddressBalance.ts`)

**New file** — separate from `useOpenClaw.ts` because it talks to eRPC, not Kubernetes. This hook is entirely self-contained and fault-tolerant.

```ts
import { useState, useEffect } from 'react';
import type { AddressBalance } from '@/types/openclaw';

const ERPC_URL = process.env.NEXT_PUBLIC_ERPC_URL ?? 'https://obol.stack/rpc';
const NETWORK_PRIORITY = ['mainnet', 'sepolia', 'hoodi'];
const TIMEOUT_MS = 3000;

/**
 * Fetches ETH balance for an address across connected eRPC networks.
 * Returns the first network with a non-zero balance (mainnet preferred).
 * Returns null if eRPC is unreachable or any error occurs — never throws.
 */
export function useAddressBalance(address: string | null): {
  balance: AddressBalance | null;
} {
  const [balance, setBalance] = useState<AddressBalance | null>(null);

  useEffect(() => {
    if (!address) return;

    const controller = new AbortController();

    async function fetchBalance() {
      try {
        // 1. Discover healthy networks from eRPC status
        const statusRes = await fetch(ERPC_URL, {
          signal: controller.signal,
          // Short timeout via AbortController — see setTimeout below
        });
        if (!statusRes.ok) return;

        const status = await statusRes.json();
        const healthyNetworks: string[] = (status.networks ?? [])
          .filter((n: { state: string }) => n.state === 'OK')
          .map((n: { alias: string }) => n.alias);

        // 2. Sort by priority: mainnet first, then alphabetical testnets
        const sorted = NETWORK_PRIORITY.filter(n =>
          healthyNetworks.includes(n)
        );
        if (sorted.length === 0) return;

        // 3. Check balance on each network (sequentially to avoid hammering)
        for (const network of sorted) {
          try {
            const rpcRes = await fetch(`${ERPC_URL}/${network}`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              signal: controller.signal,
              body: JSON.stringify({
                jsonrpc: '2.0',
                id: 1,
                method: 'eth_getBalance',
                params: [address, 'latest'],
              }),
            });

            if (!rpcRes.ok) continue;

            const rpcData = await rpcRes.json();
            if (rpcData.error) continue;

            const weiHex = rpcData.result;
            const wei = BigInt(weiHex);
            if (wei > 0n) {
              setBalance({
                balance: formatEth(wei),
                network,
              });
              return; // Stop at first network with funds
            }
          } catch {
            continue; // This network failed — try next
          }
        }

        // 4. All networks returned 0 — show 0 on the highest-priority network
        setBalance({ balance: '0', network: sorted[0] });
      } catch {
        // eRPC entirely unreachable — that's fine, just don't show balance
        setBalance(null);
      }
    }

    // Global timeout — if the whole thing takes >3s, give up
    const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
    fetchBalance().finally(() => clearTimeout(timer));

    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [address]);

  return { balance };
}

function formatEth(wei: bigint): string {
  const eth = Number(wei) / 1e18;
  if (eth === 0) return '0';
  if (eth < 0.001) return '<0.001';
  return eth.toFixed(3).replace(/\.?0+$/, '');
}
```

**Key resilience patterns**:

- `AbortController` with 3s timeout — prevents the card from waiting forever
- Every `fetch` and JSON parse is wrapped in try/catch — any failure at any step returns `null`
- Networks are tried sequentially, not in parallel — avoids hammering a struggling eRPC
- Individual network failures are caught and skipped — if mainnet RPC fails but hoodi works, hoodi balance is shown
- The hook only fires when `address` is non-null — no wasted requests before wallet data loads
- Cleanup aborts in-flight requests on unmount

### 5. OpenClawInstanceCard (`src/components/molecules/OpenClawInstanceCard/OpenClawInstanceCard.tsx`)

Modify the existing component:

1. Import and call `useWeb3SignerMetadata(instance.id)`
2. Import and call `useAddressBalance(primaryAddress)` — where `primaryAddress` is derived from metadata
3. Add a truncation helper: `truncateAddress(addr)` → `0xAbCd...Ef12`
4. Add a copy-to-clipboard handler using `navigator.clipboard.writeText()`
5. Render a new `MetaItem` row between the model row and `ActionsRow`:

```tsx
const primaryAddress = metadata?.addresses?.[0]?.address ?? null;
const { balance: balanceInfo } = useAddressBalance(primaryAddress);

// ... in the JSX, between model MetaItem and ActionsRow:

{
  metadata && metadata.addresses.length > 0 && (
    <MetaItem>
      <MetaLabel>Wallet:</MetaLabel>
      <MetaValue
        onClick={() =>
          navigator.clipboard.writeText(metadata.addresses[0].address)
        }
        css={{ cursor: 'pointer', '&:hover': { color: '$obolGreen' } }}
      >
        {truncateAddress(metadata.addresses[0].address)}
      </MetaValue>
      {metadata.count > 1 && (
        <CountBadge count={metadata.count - 1} label="keys" />
      )}
      {balanceInfo && (
        <Text css={{ fontSize: '$1', color: '$body', marginLeft: '$xxs' }}>
          {balanceInfo.balance} ETH ({balanceInfo.network})
        </Text>
      )}
    </MetaItem>
  );
}
```

**Note**: `useAddressBalance(null)` is called when metadata hasn't loaded yet. The hook short-circuits immediately (`if (!address) return`) so this is safe and doesn't violate the rules of hooks.

### 6. No other components change

The `OpenClawList`, `DashboardContent`, and `DashboardTemplate` components don't need any modification. The wallet data and balance are fetched and rendered entirely within `OpenClawInstanceCard`.

## File Change Summary

| File                                                                     | Change                                                                     |
| ------------------------------------------------------------------------ | -------------------------------------------------------------------------- |
| `src/types/openclaw.ts`                                                  | Add `Web3SignerAddress`, `Web3SignerMetadata`, `AddressBalance` interfaces |
| `src/app/api/openclaw/wallet/route.ts`                                   | **New file** — API route to fetch ConfigMap                                |
| `src/hooks/useOpenClaw.ts`                                               | Add `useWeb3SignerMetadata()` hook                                         |
| `src/hooks/useAddressBalance.ts`                                         | **New file** — client-side balance hook via eRPC                           |
| `src/components/molecules/OpenClawInstanceCard/OpenClawInstanceCard.tsx` | Add wallet + balance row using new hooks                                   |
