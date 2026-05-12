import { connectorsForWallets } from '@rainbow-me/rainbowkit';
import {
  metaMaskWallet,
  rabbyWallet,
  coinbaseWallet,
  safeWallet,
  walletConnectWallet,
  binanceWallet,
} from '@rainbow-me/rainbowkit/wallets';
import { defineChain } from 'viem';
import { createConfig, http } from 'wagmi';
import { mainnet, sepolia, base, baseSepolia } from 'wagmi/chains';

// Route RPC calls through the local eRPC gateway where available to avoid
// CORS issues with public RPCs (e.g. eth.merkle.io).
const ERPC_URL = process.env.NEXT_PUBLIC_ERPC_URL ?? '/rpc';

// Gentle retry settings — the dashboard is informational, not transactional.
// Avoid hammering endpoints when the stack or a network isn't up yet.
const rpcOpts = { retryCount: 1, retryDelay: 2000, timeout: 5000 } as const;

export const hoodi = defineChain({
  id: 560048,
  name: 'Hoodi Network',
  nativeCurrency: {
    name: 'Ether',
    symbol: 'ETH',
    decimals: 18,
  },
  rpcUrls: {
    default: {
      http: ['https://ethereum-hoodi-rpc.publicnode.com'],
    },
    public: {
      http: ['https://ethereum-hoodi-rpc.publicnode.com'],
    },
  },
  blockExplorers: {
    default: {
      name: 'Hoodi Explorer',
      url: 'https://hoodi.etherscan.io',
    },
  },
});

// Per-chain transports. Sepolia doesn't have an eRPC upstream yet,
// so fall back to the chain default (publicnode) for now.
const transports = {
  [mainnet.id]: http(`${ERPC_URL}/mainnet`, rpcOpts),
  [sepolia.id]: http(undefined, rpcOpts), // chain default (no eRPC upstream)
  [base.id]: http(`${ERPC_URL}/base`, rpcOpts),
  [baseSepolia.id]: http(`${ERPC_URL}/base-sepolia`, rpcOpts),
  [hoodi.id]: http(`${ERPC_URL}/hoodi`, rpcOpts),
} as const;

// WalletConnect requires a project ID from https://cloud.walletconnect.com
// This should be set via NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID environment variable
const walletConnectProjectId =
  process.env.NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID;

// Helper function to check if WalletConnect is configured
export function isWalletConnectConfigured(): boolean {
  return !!walletConnectProjectId;
}

// Lazy initialization to prevent indexedDB access during SSR
let wagmiConfig: ReturnType<typeof createConfig> | null = null;

function getConfig() {
  // Only create connectors on the client side to avoid indexedDB errors during SSR
  // WalletConnect uses indexedDB which is not available during SSR
  if (typeof window === 'undefined') {
    // Return a minimal config for SSR that doesn't use connectors
    return createConfig({
      chains: [mainnet, sepolia, hoodi, base, baseSepolia],
      transports,
      ssr: true,
    });
  }

  // Create config on client side only
  if (!wagmiConfig) {
    // Only initialize connectors if WalletConnect is configured AND we're in the browser
    // Otherwise, create a minimal config without wallet connectors
    const connectors =
      walletConnectProjectId && typeof window !== 'undefined'
        ? connectorsForWallets(
            [
              {
                groupName: 'Recommended',
                wallets: [
                  metaMaskWallet,
                  rabbyWallet,
                  coinbaseWallet,
                  safeWallet,
                  walletConnectWallet,
                  binanceWallet,
                ],
              },
            ],
            {
              appName: 'Obol Stack Dashboard',
              projectId: walletConnectProjectId,
            }
          )
        : undefined;

    wagmiConfig = connectors
      ? createConfig({
          connectors,
          chains: [mainnet, sepolia, hoodi, base, baseSepolia],
          transports,
          ssr: true,
        })
      : createConfig({
          chains: [mainnet, sepolia, hoodi, base, baseSepolia],
          transports,
          ssr: true,
        });
  }

  return wagmiConfig;
}

// Export config - getConfig() will return SSR-safe config during server-side rendering
// and create connectors only on the client side
export const config = getConfig();
