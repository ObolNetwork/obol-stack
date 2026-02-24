export interface OpenClawInstance {
  id: string;
  namespace: string;
  hostname: string;
  status: 'running' | 'pending' | 'error' | 'unknown';
}

export interface OpenClawInstanceHealth {
  id: string;
  healthy: boolean;
  model: string;
  provider?: string;
  endpoint: string;
  podStatus?: string;
  restarts?: number;
  error?: string;
}

export interface OpenClawToken {
  token: string;
}

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
