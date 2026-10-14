export type AgentRuntime = 'hermes';

export const DEFAULT_AGENT_DOMAIN = 'obol.stack';
export const DEFAULT_AGENT_ID = 'obol-agent';
export const HERMES_PORT = 8642;

export interface AgentRuntimeConfig {
  namespacePrefix: string;
  serviceName: string;
  port: number;
  displayName: string;
  podSelector: string;
  configMapName: string;
  configKey: string;
  tokenSecretName: string;
  tokenKey: string;
}

export const AGENT_RUNTIME_CONFIG: Record<AgentRuntime, AgentRuntimeConfig> = {
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
  runtime: AgentRuntime = 'hermes'
): string {
  return `${AGENT_RUNTIME_CONFIG[runtime].namespacePrefix}${id}`;
}

export function getAgentIdFromNamespace(
  namespace: string,
  runtime: AgentRuntime = 'hermes'
): string {
  return namespace.replace(AGENT_RUNTIME_CONFIG[runtime].namespacePrefix, '');
}

export function getRuntimeFromNamespace(
  namespace: string
): AgentRuntime | null {
  if (namespace.startsWith(AGENT_RUNTIME_CONFIG.hermes.namespacePrefix)) {
    return 'hermes';
  }
  return null;
}

export function getAgentDashboardUrl(
  id: string,
  runtime: AgentRuntime = 'hermes',
  domain = DEFAULT_AGENT_DOMAIN
): string {
  if (id === DEFAULT_AGENT_ID) {
    return `http://${DEFAULT_AGENT_ID}.${domain}`;
  }
  return `http://${getAgentNamespace(id, runtime)}-ui.${domain}`;
}

export function getAgentDashboardHostname(
  id: string,
  runtime: AgentRuntime = 'hermes',
  domain = DEFAULT_AGENT_DOMAIN
): string {
  return getAgentDashboardUrl(id, runtime, domain).replace(/^https?:\/\//, '');
}

export function isDefaultSellAgent({
  id,
}: {
  id: string;
  runtime: AgentRuntime;
}): boolean {
  return id === DEFAULT_AGENT_ID;
}

export function getAgentServiceUrl({
  id,
  runtime = 'hermes',
  isInCluster,
  hermesLocalUrl,
}: {
  id: string;
  runtime?: AgentRuntime;
  isInCluster: boolean;
  hermesLocalUrl?: string;
}): string {
  const runtimeConfig = AGENT_RUNTIME_CONFIG[runtime];
  const namespace = getAgentNamespace(id, runtime);

  if (isInCluster) {
    return `http://${runtimeConfig.serviceName}.${namespace}.svc.cluster.local:${runtimeConfig.port}`;
  }

  return hermesLocalUrl || `http://localhost:${HERMES_PORT}`;
}
