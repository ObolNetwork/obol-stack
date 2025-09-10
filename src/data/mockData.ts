import { DashboardData } from '../types/dashboard';

export const mockDashboardData: DashboardData = {
  header: {
    network: 'mainnet',
    nodeStatus: 'online',
  },
  ethereum: {
    status: 'synced',
    blocks: 18972341,
    finalized: 254170,
    participation: 97.6,
    clients: 64,
    distance: 0, // Distance to justified epoch
  },
  aztec: {
    status: 'synced',
    blocks: 245621,
    finalized: 245618,
    participation: 95.2,
    clients: 28,
    distance: 0, // Distance to justified epoch
  },
  nodeOverview: {
    diskUsage: {
      value: 1247.8,
      total: 2000,
      percentage: 62.4,
      unit: 'GB',
    },
    memoryUsage: {
      value: 6.2,
      total: 32,
      percentage: 19.4,
      unit: 'GB',
    },
    cpuLoad: {
      percentage: 18.5,
    },
    uptime: {
      hours: 76, // 3 days, 4 hours
      status: 'continuous operation',
    },
  },
  clients: {
    nethermind: {
      version: 'v1.32.4',
      peers: 25,
      status: 'synced',
    },
    nimbus: {
      version: 'v25.6.0',
      peers: 39,
      status: 'synced',
    },
  },
  ports: {
    p2p: { port: 30303, status: 'open' },
    jsonRpc: { port: 8545, status: 'closed' },
    prometheus: { port: 9090, status: 'closed' },
    beaconApi: { port: 4000, status: 'open' },
  },
};
