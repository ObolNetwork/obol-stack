export interface WalletAddress {
  address: string;
  publicKey: string;
  createdAt: string;
  label: string;
}

export interface WalletMetadata {
  instanceId: string;
  addresses: WalletAddress[];
  count: number;
}

export interface AddressBalance {
  balance: string;
  network: string;
}
