/**
 * Environment configuration
 */

export const config = {
  // Service URLs - Using Kubernetes internal DNS when running in cluster
  prometheusUrl:
    process.env.PROMETHEUS_URL ||
    'http://monitoring-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090',
  grafanaUrl:
    process.env.GRAFANA_URL ||
    'http://monitoring-grafana.monitoring.svc.cluster.local',
  heliosClientUrl:
    process.env.HELIOS_CLIENT_URL ||
    'http://l1-light-client-helios.l1.svc.cluster.local:8545',
  heliosBeaconUrl:
    process.env.HELIOS_BEACON_URL ||
    'http://l1-light-client-beacon.l1.svc.cluster.local:5052',
  fullNodeExecutionUrl:
    process.env.FULL_NODE_EXECUTION_URL ||
    'http://l1-full-node-mainnet-execution.l1.svc.cluster.local:8545',
  fullNodeBeaconUrl:
    process.env.FULL_NODE_BEACON_URL ||
    'http://l1-full-node-mainnet-beacon.l1.svc.cluster.local:5052',

  // Aztec L2 Service URLs
  aztecFullNodeUrl:
    process.env.AZTEC_FULLNODE_URL ||
    'http://l2-full-node-sepolia-node.aztec-testnet.svc.cluster.local:8080',
  aztecSequencerUrl: process.env.AZTEC_SEQUENCER_URL || 'http://localhost:8080',
  aztecProverNodeUrl:
    process.env.AZTEC_PROVER_NODE_URL ||
    'http://l2-prover-node-sepolia-node.aztec-testnet.svc.cluster.local:8080',
  aztecProverBrokerUrl:
    process.env.AZTEC_PROVER_BROKER_URL ||
    'http://l2-prover-node-sepolia-broker.aztec-testnet.svc.cluster.local:8080',
  aztecProverAgentUrl:
    process.env.AZTEC_PROVER_AGENT_URL ||
    'http://l2-prover-node-sepolia-agent.aztec-testnet.svc.cluster.local:8080',

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
  },
};

export type Config = typeof config;
