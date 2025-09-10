/**
 * Environment configuration
 */

export const config = {
  // Service URLs - Using Kubernetes internal DNS when running in cluster
  prometheusUrl: process.env.PROMETHEUS_URL || 'http://monitoring-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090',
  erpcUrl: process.env.ERPC_URL || 'http://rpc.l1.svc.cluster.local',
  grafanaUrl: process.env.GRAFANA_URL || 'http://monitoring-grafana.monitoring.svc.cluster.local',
  beaconApiUrl: process.env.BEACON_API_URL || 'http://l1-full-node-beacon.l1.svc.cluster.local:5052',
  executionRpcUrl: process.env.EXECUTION_RPC_URL || 'http://l1-full-node-execution.l1.svc.cluster.local:8545',
  
  // Network Configuration
  defaultNetwork: process.env.DEFAULT_NETWORK || 'hoodi',
  networks: {
    mainnet: {
      chainId: parseInt(process.env.MAINNET_CHAIN_ID || '1'),
      name: 'Ethereum Mainnet',
      rpcPath: '/rpc/mainnet',
    },
    hoodi: {
      chainId: parseInt(process.env.HOODI_CHAIN_ID || '560048'),
      name: 'Hoodi Testnet',
      rpcPath: '/rpc/hoodi',
    },
  },
  
  // Update Intervals
  intervals: {
    block: parseInt(process.env.BLOCK_UPDATE_INTERVAL || '12000'),
    metrics: parseInt(process.env.METRICS_UPDATE_INTERVAL || '5000'),
    system: parseInt(process.env.SYSTEM_UPDATE_INTERVAL || '10000'),
  },
  
  // Feature Flags
  features: {
    realMetrics: process.env.ENABLE_REAL_METRICS === 'true',
    websocket: process.env.ENABLE_WEBSOCKET === 'true',
    aztecMock: process.env.ENABLE_AZTEC_MOCK !== 'false', // Default to true
  },
  
  // API Routes
  api: {
    dashboard: '/api/dashboard',
    system: '/api/metrics/system',
    ethereum: '/api/metrics/ethereum',
    clients: '/api/metrics/clients',
    ports: '/api/health/ports',
  },
};

export type Config = typeof config;