export interface ClientInfo {
  name: string;
  version: string;
  peers: number;
  status: 'synced' | 'syncing' | 'error';
  role: 'execution' | 'consensus' | 'validator';
  icon?: string; // URL to client icon
}

export interface ChainData {
  chainName: 'ethereum' | 'aztec'; // Add more chains here as needed
  network: 'mainnet' | 'testnet';
  nodeMode: 'fullMode' | 'lightMode';
  status: 'synced' | 'syncing' | 'error';
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
    p2p: { port: number; status: 'open' | 'closed' };
    jsonRpc: { port: number; status: 'open' | 'closed' };
    prometheus: { port: number; status: 'open' | 'closed' };
    beaconApi: { port: number; status: 'open' | 'closed' };
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
