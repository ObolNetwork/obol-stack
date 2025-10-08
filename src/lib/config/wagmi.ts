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

// Use a placeholder project ID if not provided (required for builds)
// This should be replaced with a real project ID from https://cloud.walletconnect.com
const walletConnectProjectId =
  process.env.NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID ||
  '2f5c0e3b4d6a8f9e1c3b7d5a4e2f8c9d'; // Placeholder for builds

const connectors = connectorsForWallets(
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
);

export const config = createConfig({
  connectors,
  chains: [mainnet, sepolia, hoodi],
  transports: {
    [mainnet.id]: http(),
    [sepolia.id]: http(),
    [hoodi.id]: http(),
  },
  ssr: true,
});
