import { DashboardData } from '../types/dashboard';

export const mockDashboardData: DashboardData = {
  chains: [
    {
      chainName: 'ethereum',
      network: 'mainnet',
      nodeMode: 'fullMode',
      status: 'synced',
      blocks: 18972341,
      finalized: 254170,
      participation: 97.6,
      clients: [
        {
          name: 'Nethermind',
          version: 'v1.32.4',
          peers: 25,
          status: 'synced',
          role: 'execution',
          icon: 'https://raw.githubusercontent.com/NethermindEth/nethermind/master/nethermind.png',
        },
        {
          name: 'Nimbus',
          version: 'v25.6.0',
          peers: 39,
          status: 'synced',
          role: 'consensus',
          icon: 'https://nimbus.team/img/nimbus-logo.svg',
        },
      ],
      distance: 0,
    },
    {
      chainName: 'aztec',
      network: 'testnet',
      nodeMode: 'fullMode',
      status: 'synced',
      blocks: 245621,
      finalized: 245618,
      participation: 95.2,
      clients: [
        {
          name: 'Aztec Node',
          version: 'v0.24.0',
          peers: 28,
          status: 'synced',
          role: 'execution',
          icon: 'https://aztec.network/img/aztec-logo.png',
        },
      ],
      distance: 0,
    },
  ],
  nodeOverview: {
    diskUsage: {
      value: 1248,
      total: 2000,
      percentage: 62,
      unit: 'GB',
    },
    memoryUsage: {
      value: 6,
      total: 32,
      percentage: 19,
      unit: 'GB',
    },
    cpuLoad: {
      percentage: 19,
    },
    uptime: {
      hours: 76,
      status: 'online',
    },
  },
  ports: {
    p2p: { port: 30303, status: 'open' },
    jsonRpc: { port: 8545, status: 'closed' },
    prometheus: { port: 9090, status: 'closed' },
    beaconApi: { port: 5052, status: 'open' },
  },
};
