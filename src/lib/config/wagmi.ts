import { connectorsForWallets } from '@rainbow-me/rainbowkit';
import {
  metaMaskWallet,
  rabbyWallet,
  coinbaseWallet,
  safeWallet,
  walletConnectWallet,
} from '@rainbow-me/rainbowkit/wallets';
import { defineChain } from 'viem';
import { createConfig, http } from 'wagmi';
import { mainnet, sepolia } from 'wagmi/chains';

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
      chains: [mainnet, sepolia, hoodi],
      transports: {
        [mainnet.id]: http(),
        [sepolia.id]: http(),
        [hoodi.id]: http(),
      },
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
          chains: [mainnet, sepolia, hoodi],
          transports: {
            [mainnet.id]: http(),
            [sepolia.id]: http(),
            [hoodi.id]: http(),
          },
          ssr: true,
        })
      : createConfig({
          chains: [mainnet, sepolia, hoodi],
          transports: {
            [mainnet.id]: http(),
            [sepolia.id]: http(),
            [hoodi.id]: http(),
          },
          ssr: true,
        });
  }

  return wagmiConfig;
}

// Export config - getConfig() will return SSR-safe config during server-side rendering
// and create connectors only on the client side
export const config = getConfig();
