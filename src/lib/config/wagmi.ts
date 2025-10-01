import { connectorsForWallets } from '@rainbow-me/rainbowkit';
import { metaMaskWallet } from '@rainbow-me/rainbowkit/wallets';
import { createConfig, http } from 'wagmi';
import { mainnet, sepolia, hoodi } from 'wagmi/chains';

const connectors = connectorsForWallets(
  [
    {
      groupName: 'Recommended',
      wallets: [metaMaskWallet],
    },
  ],
  {
    appName: 'Obol Stack Dashboard',
    projectId:
      process.env.NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID ||
      '2f5a2b1c8e3d4f6a7b8c9d0e1f2a3b4c',
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
