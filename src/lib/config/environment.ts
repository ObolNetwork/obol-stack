/**
 * Environment configuration
 */

export const config = {
  // Service URLs - Using Kubernetes internal DNS when running in cluster
  prometheusUrl:
    process.env.NEXT_PUBLIC_PROMETHEUS_URL ||
    'http://monitoring-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090',
  heliosClientUrl:
    process.env.NEXT_PUBLIC_HELIOS_CLIENT_URL ||
    'http://l1-light-client-helios.l1.svc.cluster.local:8545',
  fullNodeExecutionUrl:
    process.env.NEXT_PUBLIC_FULL_NODE_EXECUTION_URL ||
    'http://l1-full-node-mainnet-execution.l1.svc.cluster.local:8545',
  fullNodeBeaconUrl:
    process.env.NEXT_PUBLIC_FULL_NODE_BEACON_URL ||
    'http://l1-full-node-mainnet-beacon.l1.svc.cluster.local:5052',

  // Aztec L2 Service URLs
  aztecFullNodeUrl:
    process.env.NEXT_PUBLIC_AZTEC_FULLNODE_URL ||
    'http://l2-full-node-sepolia-node.aztec-testnet.svc.cluster.local:8080',
  aztecSequencerUrl:
    process.env.NEXT_PUBLIC_AZTEC_SEQUENCER_URL || 'http://localhost:8080',
  aztecProverNodeUrl:
    process.env.NEXT_PUBLIC_AZTEC_PROVER_NODE_URL ||
    'http://l2-prover-node-sepolia-node.aztec-testnet.svc.cluster.local:8080',
  aztecProverBrokerUrl:
    process.env.NEXT_PUBLIC_AZTEC_PROVER_BROKER_URL ||
    'http://l2-prover-node-sepolia-broker.aztec-testnet.svc.cluster.local:8080',
  aztecProverAgentUrl:
    process.env.NEXT_PUBLIC_AZTEC_PROVER_AGENT_URL ||
    'http://l2-prover-node-sepolia-agent.aztec-testnet.svc.cluster.local:8080',

  erpcUrl:
    process.env.NEXT_PUBLIC_ERPC_URL ||
    'http://erpc.default.svc.cluster.local:4000/rpc/',
};

export type Config = typeof config;
