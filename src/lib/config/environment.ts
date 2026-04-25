/**
 * Environment configuration
 */

export const config = {
  // Service URLs - Using Kubernetes internal DNS when running in cluster
  prometheusUrl:
    process.env.NEXT_PUBLIC_PROMETHEUS_URL ||
    'http://monitoring-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090',
  erpcUrl: process.env.NEXT_PUBLIC_ERPC_URL || 'https://obol.stack/rpc',
  // Server-side base URL for the eRPC status endpoint (GET /).
  // Defaults to the in-cluster service; override with ERPC_BASE_URL for local port-forwarding.
  erpcBaseUrl:
    process.env.ERPC_BASE_URL || 'http://erpc.erpc.svc.cluster.local',
  agentIngressBaseDomain: process.env.AGENT_INGRESS_BASE_DOMAIN || 'obol.stack',
  openclawUrl: process.env.OPENCLAW_URL || `http://localhost:18789`,
  isInCluster: !!process.env.KUBERNETES_SERVICE_HOST,
  commitSha: process.env.NEXT_PUBLIC_COMMIT_SHA || 'local',
  tagVersion: process.env.NEXT_PUBLIC_TAG_VERSION || 'local',
  buildTime: process.env.NEXT_PUBLIC_BUILD_TIME || '',
};

export type Config = typeof config;
