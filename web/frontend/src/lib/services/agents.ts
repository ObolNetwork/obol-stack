import { CoreV1Api } from '@kubernetes/client-node';
import { ObjectCoreV1Api } from '@kubernetes/client-node/dist/gen/types/ObjectParamAPI';
import type {
  AgentInstance,
  AgentInstanceHealth,
  AgentRuntime,
} from '@/types/agent';
import type { WalletMetadata } from '@/types/wallet';
import { config } from '@/lib/config/environment';
import {
  DEFAULT_AGENT_ID,
  getAgentDashboardHostname,
} from '@/lib/services/agent-runtime';
import { sharedKubeConfig } from '@/lib/services/kube';

const DNS1123_LABEL_RE = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/;

export function isDns1123Label(value: string): boolean {
  return DNS1123_LABEL_RE.test(value);
}

const RUNTIME_CONFIG = {
  hermes: {
    namespacePrefix: 'hermes-',
    serviceName: 'hermes',
    configMapName: 'hermes-config',
    secretName: 'hermes-api-server',
    secretKey: 'API_SERVER_KEY',
    port: 8642,
  },
} as const;

type RuntimeDescriptor = (typeof RUNTIME_CONFIG)[AgentRuntime];

export class AgentClient {
  private coreV1Api: ObjectCoreV1Api;

  private isInCluster: boolean;

  constructor() {
    const kc = sharedKubeConfig();
    this.coreV1Api = kc.makeApiClient(CoreV1Api);
    this.isInCluster = !!process.env.KUBERNETES_SERVICE_HOST;
  }

  private descriptor(runtime: AgentRuntime): RuntimeDescriptor {
    return RUNTIME_CONFIG[runtime];
  }

  private parseRuntime(namespace: string): AgentRuntime | null {
    if (namespace.startsWith(RUNTIME_CONFIG.hermes.namespacePrefix)) {
      return 'hermes';
    }
    return null;
  }

  private instanceFromNamespace(namespace: string): AgentInstance | null {
    // Namespaces end up in service hostnames/URLs — reject anything that is
    // not a DNS-1123 label so caller-supplied ids can't redirect requests.
    if (!isDns1123Label(namespace)) return null;
    const runtime = this.parseRuntime(namespace);
    if (!runtime) {
      return null;
    }

    const id = namespace.replace(this.descriptor(runtime).namespacePrefix, '');

    return {
      id,
      runtime,
      namespace,
      hostname: `${namespace}.${config.agentIngressBaseDomain}`,
      dashboardHostname: getAgentDashboardHostname(
        id,
        runtime,
        config.agentIngressBaseDomain
      ),
      status: 'unknown',
    };
  }

  public getServiceUrl(instance: AgentInstance): string {
    const desc = this.descriptor(instance.runtime);
    if (this.isInCluster) {
      return `http://${desc.serviceName}.${instance.namespace}.svc.cluster.local:${desc.port}`;
    }
    return `http://${instance.namespace}.${config.agentIngressBaseDomain}`;
  }

  public getPublicUrl(instance: AgentInstance): string {
    return `http://${instance.hostname}`;
  }

  /** `obol agent new <name>` — namespaces `agent-<name>`, not `hermes-*`. */
  private instanceFromSubAgentNamespace(
    namespace: string
  ): AgentInstance | null {
    const prefix = 'agent-';
    if (!namespace.startsWith(prefix) || !isDns1123Label(namespace))
      return null;
    const id = namespace.slice(prefix.length);
    if (!id) return null;

    return {
      id,
      runtime: 'hermes',
      namespace,
      hostname: `${namespace}.${config.agentIngressBaseDomain}`,
      status: 'unknown',
      isSubAgent: true,
    };
  }

  /** Resolve by Kubernetes namespace (authoritative for status lookups). */
  public resolveInstanceByNamespace(namespace: string): AgentInstance | null {
    return (
      this.instanceFromSubAgentNamespace(namespace) ??
      this.instanceFromNamespace(namespace)
    );
  }

  /** Master Hermes (`hermes-<id>`) or sub-agent (`agent-<id>`). */
  public resolveInstance(
    _runtime: AgentRuntime,
    id: string
  ): AgentInstance | null {
    if (id === DEFAULT_AGENT_ID) {
      return this.instanceFromNamespace(`hermes-${id}`);
    }
    // Sub-agents live in agent-<name>. Try before hermes-<id> — otherwise
    // id "agent-v0-10-0" wrongly maps to namespace hermes-agent-v0-10-0.
    return (
      this.instanceFromSubAgentNamespace(`agent-${id}`) ??
      this.instanceFromNamespace(`hermes-${id}`)
    );
  }

  public async listInstances(): Promise<AgentInstance[]> {
    const response = await this.coreV1Api.listNamespace({});
    const names = response.items
      .map(ns => ns.metadata?.name)
      .filter((name): name is string => Boolean(name));

    const runtimeInstances = names
      .map(name => this.instanceFromNamespace(name))
      .filter((instance): instance is AgentInstance => instance !== null);

    const subAgents = names
      .map(name => this.instanceFromSubAgentNamespace(name))
      .filter((instance): instance is AgentInstance => instance !== null);

    const discovered = [...runtimeInstances, ...subAgents];

    const hydrated = await Promise.all(
      discovered.map(async instance => {
        const healthy = await this.checkHealth(instance);
        return {
          ...instance,
          status: healthy ? ('running' as const) : ('unknown' as const),
        };
      })
    );

    hydrated.sort((a, b) => {
      const aDefault =
        a.runtime === 'hermes' && a.id === DEFAULT_AGENT_ID ? -1 : 0;
      const bDefault =
        b.runtime === 'hermes' && b.id === DEFAULT_AGENT_ID ? -1 : 0;
      if (aDefault !== bDefault) {
        return aDefault - bDefault;
      }

      return a.id.localeCompare(b.id);
    });

    return hydrated;
  }

  public async getInstanceHealth(
    runtime: AgentRuntime,
    id: string,
    namespace?: string
  ): Promise<AgentInstanceHealth> {
    const instance = namespace
      ? this.resolveInstanceByNamespace(namespace)
      : this.resolveInstance(runtime, id);
    if (!instance) {
      return {
        id,
        runtime,
        healthy: false,
        model: 'unknown',
        endpoint: '',
        error: `Unsupported runtime '${runtime}'`,
      };
    }

    try {
      await this.coreV1Api.readNamespace({ name: instance.namespace });
    } catch {
      return {
        id,
        runtime,
        healthy: false,
        model: 'unknown',
        endpoint: '',
        error: `Instance '${id}' not found`,
      };
    }

    const [podInfo, token, configInfo] = await Promise.all([
      this.getPodInfo(instance),
      this.getGatewayToken(instance),
      this.getConfigInfo(instance),
    ]);

    let apiConnected: boolean | undefined;
    let apiError: string | undefined;
    let advertisedModel: string | undefined;

    if (instance.isSubAgent) {
      // Sub-agents are cluster-internal only; pod phase is the reachable signal in local dev.
      const resolvedModel = configInfo.model || 'unknown';
      return {
        id,
        runtime,
        healthy: podInfo.running,
        model: resolvedModel,
        provider: configInfo.provider || inferProvider(resolvedModel),
        endpoint: this.getPublicUrl(instance),
        podStatus: podInfo.phase,
        restarts: podInfo.restarts,
        error:
          podInfo.phase === 'NotFound'
            ? `Instance '${id}' not found`
            : undefined,
        apiConnected,
        apiError,
      };
    }

    if (token) {
      const modelInfo = await this.fetchModels(
        this.getServiceUrl(instance),
        token
      );
      apiConnected = modelInfo.reachable;
      apiError = modelInfo.error;
      advertisedModel = modelInfo.model;
    } else {
      apiConnected = false;
      apiError = 'API token not found';
    }

    const resolvedModel = advertisedModel || configInfo.model || 'unknown';
    const resolvedProvider =
      configInfo.provider || inferProvider(resolvedModel);

    return {
      id,
      runtime,
      healthy: podInfo.running && apiConnected,
      model: resolvedModel,
      provider: resolvedProvider,
      endpoint: this.getPublicUrl(instance),
      podStatus: podInfo.phase,
      restarts: podInfo.restarts,
      error:
        podInfo.phase === 'NotFound' ? `Instance '${id}' not found` : undefined,
      apiConnected,
      apiError,
    };
  }

  public async getGatewayToken(
    instance: AgentInstance
  ): Promise<string | null> {
    return this.getHermesAPIServerKey(instance.namespace);
  }

  /**
   * Single apiserver call that returns wallet metadata for every namespace
   * that hosts a `wallet-metadata` ConfigMap. Used by /api/agents/wallets
   * to avoid N+1 reads when the operator runs many agents.
   *
   * Returns Map<namespace, WalletMetadata>; namespaces with no parseable
   * payload are still entries (empty addresses).
   */
  public async listAllWalletMetadata(): Promise<Map<string, WalletMetadata>> {
    const out = new Map<string, WalletMetadata>();
    try {
      const response = await this.coreV1Api.listConfigMapForAllNamespaces({
        fieldSelector: 'metadata.name=wallet-metadata',
      });
      for (const cm of response.items ?? []) {
        const ns = cm.metadata?.namespace ?? '';
        if (!ns) continue;
        const raw = cm.data?.['addresses.json'];
        if (!raw) {
          out.set(ns, { instanceId: '', addresses: [], count: 0 });
          continue;
        }
        try {
          const parsed = JSON.parse(raw);
          out.set(ns, {
            instanceId: parsed.instanceId ?? '',
            addresses: parsed.addresses ?? [],
            count: parsed.count ?? parsed.addresses?.length ?? 0,
          });
        } catch {
          out.set(ns, { instanceId: '', addresses: [], count: 0 });
        }
      }
    } catch (error) {
      console.warn('[AgentClient.listAllWalletMetadata] failed:', error);
    }
    return out;
  }

  public async getWalletMetadata(
    instance: AgentInstance
  ): Promise<WalletMetadata> {
    try {
      const response = await this.coreV1Api.readNamespacedConfigMap({
        name: 'wallet-metadata',
        namespace: instance.namespace,
      });

      const raw = response.data?.['addresses.json'];
      if (!raw) {
        return { instanceId: instance.id, addresses: [], count: 0 };
      }

      const parsed = JSON.parse(raw);
      return {
        instanceId: parsed.instanceId ?? instance.id,
        addresses: parsed.addresses ?? [],
        count: parsed.count ?? parsed.addresses?.length ?? 0,
      };
    } catch {
      return { instanceId: instance.id, addresses: [], count: 0 };
    }
  }

  public async getTunnelURL(): Promise<string | null> {
    try {
      const response = await this.coreV1Api.readNamespacedConfigMap({
        name: 'obol-stack-config',
        namespace: 'obol-frontend',
      });
      return response.data?.['tunnelURL'] || null;
    } catch {
      return null;
    }
  }

  /**
   * Running Obol CLI version from obol-stack-config.
   * Written by the CLI during `obol stack up` / tunnel sync.
   */
  public async getObolStackVersion(): Promise<string | null> {
    try {
      const response = await this.coreV1Api.readNamespacedConfigMap({
        name: 'obol-stack-config',
        namespace: 'obol-frontend',
      });
      const version = response.data?.['obolVersion']?.trim();
      return version || null;
    } catch {
      return null;
    }
  }

  private async checkHealth(instance: AgentInstance): Promise<boolean> {
    if (instance.isSubAgent) {
      const podInfo = await this.getPodInfo(instance);
      return podInfo.running;
    }

    try {
      const token = await this.getGatewayToken(instance);
      if (!token) {
        return false;
      }

      const api = await this.fetchModels(this.getServiceUrl(instance), token);
      return api.reachable;
    } catch {
      return false;
    }
  }

  private async getHermesAPIServerKey(
    namespace: string
  ): Promise<string | null> {
    try {
      const response = await this.coreV1Api.readNamespacedSecret({
        name: RUNTIME_CONFIG.hermes.secretName,
        namespace,
      });

      const tokenBase64 = response.data?.[RUNTIME_CONFIG.hermes.secretKey];
      if (!tokenBase64) {
        return null;
      }

      return Buffer.from(tokenBase64, 'base64').toString('utf-8');
    } catch (error) {
      console.error(`Failed to get Hermes token for ${namespace}:`, error);
      return null;
    }
  }

  private async getConfigInfo(instance: AgentInstance): Promise<{
    model?: string;
    provider?: string;
  }> {
    try {
      const response = await this.coreV1Api.readNamespacedConfigMap({
        name: this.descriptor(instance.runtime).configMapName,
        namespace: instance.namespace,
      });

      const configData = response.data?.['config.yaml'];
      if (!configData) {
        return {};
      }

      return {
        model: matchConfigScalar(configData, 'default'),
        provider: matchConfigScalar(configData, 'provider'),
      };
    } catch (error) {
      console.error(`Failed to read config for ${instance.namespace}:`, error);
      return {};
    }
  }

  private async fetchModels(
    url: string,
    token: string
  ): Promise<{ reachable: boolean; model?: string; error?: string }> {
    try {
      const response = await fetch(`${url}/v1/models`, {
        signal: AbortSignal.timeout(3000),
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
        },
      });

      if (!response.ok) {
        return { reachable: false, error: `HTTP ${response.status}` };
      }

      const data = await response.json();
      const model = data?.data?.[0]?.id;
      return { reachable: true, model };
    } catch (error) {
      return {
        reachable: false,
        error: error instanceof Error ? error.message : 'Connection failed',
      };
    }
  }

  private async getPodInfo(
    instance: AgentInstance
  ): Promise<{ running: boolean; phase: string; restarts: number }> {
    try {
      const response = await this.coreV1Api.listNamespacedPod({
        namespace: instance.namespace,
        labelSelector: `app.kubernetes.io/name=${
          this.descriptor(instance.runtime).serviceName
        }`,
      });

      if (!response.items || response.items.length === 0) {
        return { running: false, phase: 'NotFound', restarts: 0 };
      }

      const pod = response.items[0];
      const phase = pod.status?.phase || 'Unknown';
      const restarts = pod.status?.containerStatuses?.[0]?.restartCount || 0;

      return { running: phase === 'Running', phase, restarts };
    } catch (error) {
      console.error(`Failed to get pod info for ${instance.namespace}:`, error);
      return { running: false, phase: 'Error', restarts: 0 };
    }
  }
}

function matchConfigScalar(content: string, key: string): string | undefined {
  const escaped = key.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = content.match(
    new RegExp(`^\\s*${escaped}:\\s*(.+?)\\s*$`, 'm')
  );
  if (!match) {
    return undefined;
  }

  const value = match[1]?.trim();
  if (!value) {
    return undefined;
  }

  return value.replace(/^['"]|['"]$/g, '');
}

function inferProvider(modelName: string): string | undefined {
  const name = modelName.replace(/^openai\//, '');
  if (!name || name === 'unknown') {
    return undefined;
  }
  if (name.includes('claude')) {
    return 'Anthropic';
  }
  if (/^(gpt|o[1-4])/.test(name)) {
    return 'OpenAI';
  }
  return 'Ollama';
}
