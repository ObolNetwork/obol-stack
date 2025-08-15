export interface DashboardData {
  header: {
    network: 'testnet' | 'mainnet';
    nodeStatus: 'online' | 'offline' | 'syncing';
  };
  ethereum: {
    status: 'synced' | 'syncing' | 'error';
    blocks: number;
    finalized: number;
    participation: number;
    clients: number;
    distance: number; // Distance to justified
  };
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
  clients: {
    nethermind: { 
      version: string; 
      peers: number; 
      status: 'synced' | 'syncing' | 'error'; 
    };
    nimbus: { 
      version: string; 
      peers: number; 
      status: 'synced' | 'syncing' | 'error'; 
    };
  };
  ports: {
    p2p: { port: number; status: 'open' | 'closed'; };
    jsonRpc: { port: number; status: 'open' | 'closed'; };
    prometheus: { port: number; status: 'open' | 'closed'; };
    beaconApi: { port: number; status: 'open' | 'closed'; };
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