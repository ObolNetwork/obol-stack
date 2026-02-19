// Enums and Constants
export const CHAINS = {
  ETHEREUM: 'mainnet',
  SEPOLIA: 'sepolia',
  HOODI: 'hoodi',
} as const;

// Chain ID to Chain Name mapping
export const CHAIN_ID_TO_NAME: Record<number, ChainName> = {
  1: CHAINS.ETHEREUM,
  11155111: CHAINS.SEPOLIA,
  560048: CHAINS.HOODI,
} as const;

// Helper function to get chain name by ID
export const getChainName = (chainId: ChainIdType): ChainName => {
  return CHAIN_ID_TO_NAME[chainId];
};

export const NODE_MODES = {
  FULL_MODE: 'fullMode',
  LIGHT_MODE: 'lightMode',
  AZTEC_SEQUENCER: 'aztecSequencer',
  ERPC_PROXY: 'erpcProxy',
} as const;

// App instance types matching application-instances.ts
export const APP_INSTANCE_TYPES = {
  HELIOS: 'helios',
  AZTEC: 'aztec',
  ETHEREUM: 'ethereum',
  ERPC: 'erpc',
} as const;

export type AppInstanceType =
  (typeof APP_INSTANCE_TYPES)[keyof typeof APP_INSTANCE_TYPES];

// Mapping from app instance type to node mode
export const APP_TO_NODE_MODE: Record<AppInstanceType, NodeModeType> = {
  [APP_INSTANCE_TYPES.HELIOS]: NODE_MODES.LIGHT_MODE,
  [APP_INSTANCE_TYPES.AZTEC]: NODE_MODES.AZTEC_SEQUENCER,
  [APP_INSTANCE_TYPES.ETHEREUM]: NODE_MODES.FULL_MODE,
  [APP_INSTANCE_TYPES.ERPC]: NODE_MODES.ERPC_PROXY,
} as const;

export const CLIENT_STATUS = {
  SYNCED: 'synced',
  SYNCING: 'syncing',
  ERROR: 'error',
} as const;

export const CLIENT_ROLES = {
  EXECUTION: 'execution',
  CONSENSUS: 'consensus',
  VALIDATOR: 'validator',
} as const;

export const CLIENT_NAMES = {
  RETH: 'reth',
  GETH: 'geth',
  NETHERMIND: 'nethermind',
  BESU: 'besu',
  ERIGON: 'erigon',
  ETHEREUMJS: 'ethereumjs',
  LIGHTHOUSE: 'lighthouse',
  PRYSM: 'prysm',
  TEKU: 'teku',
  NIMBUS: 'nimbus',
  LODESTAR: 'lodestar',
  GRANDINE: 'grandine',
  AZTEC: 'aztec',
  HELIOS: 'helios',
  ERPC: 'erpc',
} as const;

export const PORT_STATUS = {
  OPEN: 'open',
  CLOSED: 'closed',
} as const;

// Type definitions from constants
export type ChainIdType = keyof typeof CHAIN_ID_TO_NAME;
export type ChainName = (typeof CHAINS)[keyof typeof CHAINS];
export type NodeModeType = (typeof NODE_MODES)[keyof typeof NODE_MODES];
export type ClientStatusType =
  (typeof CLIENT_STATUS)[keyof typeof CLIENT_STATUS];
export type ClientRoleType = (typeof CLIENT_ROLES)[keyof typeof CLIENT_ROLES];
export type ClientNameType = (typeof CLIENT_NAMES)[keyof typeof CLIENT_NAMES];
export type PortStatusType = (typeof PORT_STATUS)[keyof typeof PORT_STATUS];

export interface ClientInfo {
  name: ClientNameType;
  version: string;
  peers: number;
  status: ClientStatusType;
  icon?: string; // URL to client icon
  externalUrl?: string;
  internalUrl?: string;
}

export interface AppData {
  chainName: ChainName;
  app: AppInstanceType;
  appId: string;
  nodeMode: NodeModeType;
  status: ClientStatusType;
  blocks: number;
  finalized: number;
  participation: number;
  clients: ClientInfo[]; // Array of clients for this chain
  distance: number; // Distance to justified
  upstreamCount?: number; // eRPC: number of configured upstream providers
}

export interface DashboardData {
  apps: AppData[];
  nodeOverview: {
    diskUsage: {
      value: number;
      total: number;
      percentage: number;
      unit: string;
    };
    memoryUsage: {
      value: number;
      total: number;
      percentage: number;
      unit: string;
    };
    cpuLoad: {
      percentage: number;
    };
    uptime: {
      hours: number;
      status: string;
    };
  };
  ports: {
    p2p: { port: number; status: PortStatusType };
    jsonRpc: { port: number; status: PortStatusType };
    prometheus: { port: number; status: PortStatusType };
    beaconApi: { port: number; status: PortStatusType };
  };
}

export interface MetricCardProps {
  label: string;
  value: string | number;
  unit?: string;
  percentage?: number;
  status?: 'success' | 'warning' | 'error';
  trend?: 'up' | 'down' | 'stable';
  icon?: React.ReactNode;
}
