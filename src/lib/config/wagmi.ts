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

// Only initialize connectors if WalletConnect is configured
// Otherwise, create a minimal config without wallet connectors
const connectors = walletConnectProjectId
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

// Create config with or without connectors based on availability
export const config = connectors
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
