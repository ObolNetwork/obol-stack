import { CoreV1Api, KubeConfig } from '@kubernetes/client-node';
import { ObjectCoreV1Api } from '@kubernetes/client-node/dist/gen/types/ObjectParamAPI';
import type {
  AgentInstance,
  AgentInstanceHealth,
  AgentRuntime,
} from '@/types/agent';
import type { WalletMetadata } from '@/types/openclaw';
import { config } from '@/lib/config/environment';
import {
  DEFAULT_AGENT_ID,
  getAgentDashboardHostname,
} from '@/lib/services/agent-runtime';

const RUNTIME_CONFIG = {
  hermes: {
    namespacePrefix: 'hermes-',
    serviceName: 'hermes',
    configMapName: 'hermes-config',
    secretName: 'hermes-api-server',
    secretKey: 'API_SERVER_KEY',
    port: 8642,
  },
  openclaw: {
    namespacePrefix: 'openclaw-',
    serviceName: 'openclaw',
    configMapName: 'openclaw-config',
    secretName: '',
    secretKey: 'OPENCLAW_GATEWAY_TOKEN',
    port: 18789,
  },
} as const;

const RUNTIME_ORDER: Record<AgentRuntime, number> = {
  hermes: 0,
  openclaw: 1,
};

type RuntimeDescriptor = (typeof RUNTIME_CONFIG)[AgentRuntime];

export class AgentClient {
  private coreV1Api: ObjectCoreV1Api;

  private isInCluster: boolean;

  constructor() {
    const kc = this.getKubeConfig();
    this.coreV1Api = kc.makeApiClient(CoreV1Api);
    this.isInCluster = !!process.env.KUBERNETES_SERVICE_HOST;
  }

  private getKubeConfig(): KubeConfig {
    const kc = new KubeConfig();
    if (process.env.KUBERNETES_SERVICE_HOST) {
      kc.loadFromCluster();
      return kc;
    }

    if (!process.env.NEXT_PUBLIC_KUBECONFIG) {
      throw new Error(
        'NEXT_PUBLIC_KUBECONFIG environment variable is required for local development. ' +
          'Set it to the path of your kubeconfig file.'
      );
    }

    kc.loadFromFile(process.env.NEXT_PUBLIC_KUBECONFIG);
    return kc;
  }

  private descriptor(runtime: AgentRuntime): RuntimeDescriptor {
    return RUNTIME_CONFIG[runtime];
  }

  private parseRuntime(namespace: string): AgentRuntime | null {
    if (namespace.startsWith(RUNTIME_CONFIG.hermes.namespacePrefix)) {
      return 'hermes';
    }
    if (namespace.startsWith(RUNTIME_CONFIG.openclaw.namespacePrefix)) {
      return 'openclaw';
    }
    return null;
  }

  private instanceFromNamespace(namespace: string): AgentInstance | null {
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
      dashboardHostname:
        runtime === 'hermes'
          ? getAgentDashboardHostname(
              id,
              runtime,
              config.agentIngressBaseDomain
            )
          : undefined,
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

  public async listInstances(): Promise<AgentInstance[]> {
    const response = await this.coreV1Api.listNamespace({});
    const discovered = response.items
      .map(ns => ns.metadata?.name)
      .filter((name): name is string => Boolean(name))
      .map(name => this.instanceFromNamespace(name))
      .filter((instance): instance is AgentInstance => instance !== null);

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

      const runtimeCmp = RUNTIME_ORDER[a.runtime] - RUNTIME_ORDER[b.runtime];
      if (runtimeCmp !== 0) {
        return runtimeCmp;
      }

      return a.id.localeCompare(b.id);
    });

    return hydrated;
  }

  public async getInstanceHealth(
    runtime: AgentRuntime,
    id: string
  ): Promise<AgentInstanceHealth> {
    const instance = this.instanceFromNamespace(`${runtime}-${id}`);
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

    let apiConnected = false;
    let apiError: string | undefined;
    let advertisedModel: string | undefined;

    if (token) {
      const modelInfo = await this.fetchModels(
        this.getServiceUrl(instance),
        token
      );
      apiConnected = modelInfo.reachable;
      apiError = modelInfo.error;
      advertisedModel = modelInfo.model;
    } else {
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
    if (instance.runtime === 'openclaw') {
      return this.getOpenClawGatewayToken(instance.namespace);
    }
    return this.getHermesAPIServerKey(instance.namespace);
  }

  public async getWalletMetadata(
    instance: AgentInstance
  ): Promise<WalletMetadata> {
    if (instance.runtime !== 'openclaw') {
      return { instanceId: instance.id, addresses: [], count: 0 };
    }

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

  private async checkHealth(instance: AgentInstance): Promise<boolean> {
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

  private async getOpenClawGatewayToken(
    namespace: string
  ): Promise<string | null> {
    try {
      const response = await this.coreV1Api.listNamespacedSecret({
        namespace,
        labelSelector: 'app.kubernetes.io/name=openclaw',
      });

      if (!response.items || response.items.length === 0) {
        return null;
      }

      const secret = response.items[0];
      const tokenBase64 = secret.data?.[RUNTIME_CONFIG.openclaw.secretKey];
      if (!tokenBase64) {
        return null;
      }

      return Buffer.from(tokenBase64, 'base64').toString('utf-8');
    } catch (error) {
      console.error(`Failed to get OpenClaw token for ${namespace}:`, error);
      return null;
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

      if (instance.runtime === 'openclaw') {
        const configData = response.data?.['openclaw.json'];
        if (!configData) {
          return {};
        }

        const parsed = JSON.parse(configData);
        const model = parsed?.agents?.defaults?.model?.primary;
        const providers = parsed?.models?.providers
          ? Object.keys(parsed.models.providers)
          : [];

        return {
          model,
          provider:
            providers.length === 1 ? providers[0] : inferProvider(model ?? ''),
        };
      }

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
