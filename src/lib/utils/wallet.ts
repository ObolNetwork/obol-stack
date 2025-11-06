import { WalletClient } from 'viem';
import { ChainType } from '../../types/dashboard';
import { config } from '../config/environment';

type AddEthereumChainParameter = {
  chainId: string;
  chainName: string;
  nativeCurrency: { name: string; symbol: string; decimals: number };
  rpcUrls: string[];
  blockExplorerUrls?: string[];
};

const CHAIN_PARAMS: Partial<Record<ChainType, AddEthereumChainParameter>> = {
  // Ethereum Mainnet
  ethereum: {
    chainId: '0x1',
    chainName: 'Ethereum Mainnet',
    nativeCurrency: { name: 'Ether', symbol: 'ETH', decimals: 18 },
    rpcUrls: [config.erpcUrl + '/mainnet'],
    blockExplorerUrls: ['https://etherscan.io'],
  },
  // Sepolia Testnet
  sepolia: {
    chainId: '0xaa36a7',
    chainName: 'Sepolia Testnet',
    nativeCurrency: { name: 'Ethereum', symbol: 'ETH', decimals: 18 },
    rpcUrls: [config.erpcUrl + '/sepolia'],
    blockExplorerUrls: ['https://sepolia.etherscan.io'],
  },
  // Hoodi chain (custom)
  hoodi: {
    chainId: '0x88bb0', // 560048
    chainName: 'Hoodi Network',
    nativeCurrency: { name: 'Ether', symbol: 'ETH', decimals: 18 },
    rpcUrls: [config.erpcUrl + '/hoodi'],
    blockExplorerUrls: ['https://hoodi.etherscan.io'],
  },
  // Aztec Testnet
  aztec: {
    chainId: '0xa4ef4', // Hex for 677868
    chainName: 'Aztec Testnet',
    nativeCurrency: {
      name: 'Ether',
      symbol: 'ETH',
      decimals: 18,
    },
    rpcUrls: [config.erpcUrl + '/aztec'],
    blockExplorerUrls: ['https://explorer.aztec.network'],
  },
};

export async function addNetworkToWallet(
  walletClient: WalletClient,
  chainName: ChainType
) {
  const params = CHAIN_PARAMS[chainName];

  if (!params) {
    throw new Error(
      `Network "${chainName}" is not supported for wallet addition.`
    );
  }

  await walletClient.request({
    method: 'wallet_addEthereumChain',
    params: [params],
  });
}
