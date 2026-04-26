export type AgentRuntime = 'openclaw' | 'hermes';

export const DEFAULT_AGENT_DOMAIN = 'obol.stack';
export const DEFAULT_AGENT_ID = 'obol-agent';
export const OPENCLAW_PORT = 18789;
export const HERMES_PORT = 8642;

export interface AgentRuntimeConfig {
  namespacePrefix: string;
  serviceName: string;
  port: number;
  displayName: string;
  podSelector: string;
  configMapName: string;
  configKey: string;
  tokenSecretName?: string;
  tokenSecretLabel?: string;
  tokenKey: string;
}

export const AGENT_RUNTIME_CONFIG: Record<AgentRuntime, AgentRuntimeConfig> = {
  openclaw: {
    namespacePrefix: 'openclaw-',
    serviceName: 'openclaw',
    port: OPENCLAW_PORT,
    displayName: 'OpenClaw',
    podSelector: 'app.kubernetes.io/name=openclaw',
    configMapName: 'openclaw-config',
    configKey: 'openclaw.json',
    tokenSecretLabel: 'app.kubernetes.io/name=openclaw',
    tokenKey: 'OPENCLAW_GATEWAY_TOKEN',
  },
  hermes: {
    namespacePrefix: 'hermes-',
    serviceName: 'hermes',
    port: HERMES_PORT,
    displayName: 'Hermes',
    podSelector: 'app.kubernetes.io/name=hermes',
    configMapName: 'hermes-config',
    configKey: 'config.yaml',
    tokenSecretName: 'hermes-api-server',
    tokenKey: 'API_SERVER_KEY',
  },
};

export function getAgentNamespace(
  id: string,
  runtime: AgentRuntime = 'openclaw'
): string {
  return `${AGENT_RUNTIME_CONFIG[runtime].namespacePrefix}${id}`;
}

export function getAgentIdFromNamespace(
  namespace: string,
  runtime: AgentRuntime
): string {
  return namespace.replace(AGENT_RUNTIME_CONFIG[runtime].namespacePrefix, '');
}

export function getRuntimeFromNamespace(
  namespace: string
): AgentRuntime | null {
  if (namespace.startsWith(AGENT_RUNTIME_CONFIG.hermes.namespacePrefix)) {
    return 'hermes';
  }
  if (namespace.startsWith(AGENT_RUNTIME_CONFIG.openclaw.namespacePrefix)) {
    return 'openclaw';
  }
  return null;
}

export function getAgentDashboardUrl(
  id: string,
  runtime: AgentRuntime = 'openclaw',
  domain = DEFAULT_AGENT_DOMAIN
): string {
  const namespace = getAgentNamespace(id, runtime);
  if (runtime === 'hermes') {
    if (id === DEFAULT_AGENT_ID) {
      return `http://${DEFAULT_AGENT_ID}.${domain}`;
    }
    return `http://${namespace}-ui.${domain}`;
  }
  return `http://${namespace}.${domain}`;
}

export function getAgentDashboardHostname(
  id: string,
  runtime: AgentRuntime = 'openclaw',
  domain = DEFAULT_AGENT_DOMAIN
): string {
  return getAgentDashboardUrl(id, runtime, domain).replace(/^https?:\/\//, '');
}

export function getAgentServiceUrl({
  id,
  runtime = 'openclaw',
  isInCluster,
  openclawLocalUrl,
  hermesLocalUrl,
}: {
  id: string;
  runtime?: AgentRuntime;
  isInCluster: boolean;
  openclawLocalUrl: string;
  hermesLocalUrl?: string;
}): string {
  const runtimeConfig = AGENT_RUNTIME_CONFIG[runtime];
  const namespace = getAgentNamespace(id, runtime);

  if (isInCluster) {
    return `http://${runtimeConfig.serviceName}.${namespace}.svc.cluster.local:${runtimeConfig.port}`;
  }

  if (runtime === 'hermes') {
    return hermesLocalUrl || `http://localhost:${HERMES_PORT}`;
  }

  return openclawLocalUrl;
}
