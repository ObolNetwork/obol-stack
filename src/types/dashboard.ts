// Enums and Constants
export const CHAINS = {
  ETHEREUM: 'ethereum',
  AZTEC: 'aztec',
  SEPOLIA: 'sepolia',
  HOODI: 'hoodi',
} as const;

export const NETWORKS = {
  MAINNET: 'mainnet',
  TESTNET: 'testnet',
} as const;

export const NODE_MODES = {
  FULL_MODE: 'fullMode',
  LIGHT_MODE: 'lightMode',
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

export const PORT_STATUS = {
  OPEN: 'open',
  CLOSED: 'closed',
} as const;

// Type definitions from constants
export type ChainType = (typeof CHAINS)[keyof typeof CHAINS];
export type NetworkType = (typeof NETWORKS)[keyof typeof NETWORKS];
export type NodeModeType = (typeof NODE_MODES)[keyof typeof NODE_MODES];
export type ClientStatusType =
  (typeof CLIENT_STATUS)[keyof typeof CLIENT_STATUS];
export type ClientRoleType = (typeof CLIENT_ROLES)[keyof typeof CLIENT_ROLES];
export type PortStatusType = (typeof PORT_STATUS)[keyof typeof PORT_STATUS];

export interface ClientInfo {
  name: string;
  version: string;
  peers: number;
  status: ClientStatusType;
  icon?: string; // URL to client icon
}

export interface ChainData {
  chainName: ChainType;
  network: NetworkType;
  nodeMode: NodeModeType;
  status: ClientStatusType;
  blocks: number;
  finalized: number;
  participation: number;
  clients: ClientInfo[]; // Array of clients for this chain
  distance: number; // Distance to justified
}

export interface DashboardData {
  chains: ChainData[];
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
