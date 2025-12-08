/**
 * Environment configuration
 */

export const config = {
  // Service URLs - Using Kubernetes internal DNS when running in cluster
  prometheusUrl:
    process.env.NEXT_PUBLIC_PROMETHEUS_URL ||
    'http://monitoring-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090',
  erpcUrl: process.env.NEXT_PUBLIC_ERPC_URL || 'https://obol.stack/rpc',
};

export type Config = typeof config;
