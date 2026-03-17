/**
 * OpenClaw service client for discovering and monitoring OpenClaw instances
 * Discovery: Kubernetes API (namespaces matching openclaw-*)
 * Health: HTTP fetch to OpenAI-compatible API (similar to PrometheusClient pattern)
 */

import { CoreV1Api, KubeConfig } from '@kubernetes/client-node';
import { ObjectCoreV1Api } from '@kubernetes/client-node/dist/gen/types/ObjectParamAPI';
import type {
  OpenClawInstance,
  OpenClawInstanceHealth,
  WalletMetadata,
} from '@/types/openclaw';
import { config } from '@/lib/config/environment';

const OPENCLAW_PORT = 18789;

export class OpenClawClient {
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
    } else {
      if (!process.env.NEXT_PUBLIC_KUBECONFIG) {
        throw new Error(
          'NEXT_PUBLIC_KUBECONFIG environment variable is required for local development. ' +
            'Set it to the path of your kubeconfig file.'
        );
      }
      kc.loadFromFile(process.env.NEXT_PUBLIC_KUBECONFIG);
    }
    return kc;
  }

  /**
   * Get the service URL for an OpenClaw instance
   */
  public getServiceUrl(id: string): string {
    const namespace = `openclaw-${id}`;
    if (this.isInCluster) {
      return `http://openclaw.${namespace}.svc.cluster.local:${OPENCLAW_PORT}`;
    }
    // Local dev: use port-forwarded URL (default: http://localhost:18789)
    return config.openclawUrl;
  }

  /**
   * Get the gateway token for an OpenClaw instance from Kubernetes Secret
   */
  public async getGatewayToken(namespace: string): Promise<string | null> {
    try {
      const response = await this.coreV1Api.listNamespacedSecret({
        namespace,
        labelSelector: 'app.kubernetes.io/name=openclaw',
      });

      if (!response.items || response.items.length === 0) {
        return null;
      }

      const secret = response.items[0];
      const tokenBase64 = secret.data?.['OPENCLAW_GATEWAY_TOKEN'];

      if (!tokenBase64) {
        return null;
      }

      // Decode base64
      return Buffer.from(tokenBase64, 'base64').toString('utf-8');
    } catch (error) {
      console.error(`Failed to get gateway token for ${namespace}:`, error);
      return null;
    }
  }

  /**
   * Discover OpenClaw instances from Kubernetes namespaces
   */
  async listInstances(): Promise<OpenClawInstance[]> {
    const response = await this.coreV1Api.listNamespace({});

    const openclawNamespaces = response.items.filter(ns =>
      ns.metadata?.name?.startsWith('openclaw-')
    );

    const instances: OpenClawInstance[] = await Promise.all(
      openclawNamespaces.map(async ns => {
        const namespace = ns.metadata!.name!;
        const id = namespace.replace('openclaw-', '');
        const url = this.getServiceUrl(id);

        const healthy = await this.checkHealth(namespace, url);

        return {
          id,
          namespace,
          hostname: url.replace('http://', ''),
          status: healthy ? ('running' as const) : ('unknown' as const),
        };
      })
    );

    return instances;
  }

  /**
   * Get detailed health for a specific instance
   */
  async getInstanceHealth(id: string): Promise<OpenClawInstanceHealth> {
    const namespace = `openclaw-${id}`;
    const url = this.getServiceUrl(id);

    // Check if namespace exists
    try {
      await this.coreV1Api.readNamespace({ name: namespace });
    } catch {
      return {
        id,
        healthy: false,
        model: 'unknown',
        endpoint: '',
        error: `Instance '${id}' not found`,
      };
    }

    // Get pod status, model config, and LLM health in parallel
    const [podInfo, configInfo, llmHealth] = await Promise.all([
      this.getPodInfo(namespace),
      this.getConfigInfo(namespace),
      this.getLLMHealth(),
    ]);

    return {
      id,
      healthy: podInfo.running,
      model: configInfo.model || 'unknown',
      provider: configInfo.model ? configInfo.model.split('/')[0] : undefined,
      endpoint: url,
      podStatus: podInfo.phase,
      restarts: podInfo.restarts,
      llmConnected: llmHealth.connected,
      llmError: llmHealth.error,
    };
  }

  /**
   * Get OpenClaw configuration from ConfigMap
   */
  public async getConfigInfo(
    namespace: string
  ): Promise<{ model?: string; providers?: string[] }> {
    try {
      const response = await this.coreV1Api.readNamespacedConfigMap({
        name: 'openclaw-config',
        namespace,
      });

      const configData = response.data?.['openclaw.json'];
      if (!configData) {
        return {};
      }

      const config = JSON.parse(configData);
      const model = config?.agents?.defaults?.model?.primary;
      const providers = config?.models?.providers
        ? Object.keys(config.models.providers)
        : [];

      return { model, providers };
    } catch (error) {
      console.error(`Failed to read config for ${namespace}:`, error);
      return {};
    }
  }

  /**
   * Get wallet metadata (signing addresses) from ConfigMap
   */
  public async getWalletMetadata(namespace: string): Promise<WalletMetadata> {
    try {
      const response = await this.coreV1Api.readNamespacedConfigMap({
        name: 'wallet-metadata',
        namespace,
      });

      const raw = response.data?.['addresses.json'];
      if (!raw) {
        return { instanceId: namespace, addresses: [], count: 0 };
      }

      const parsed = JSON.parse(raw);
      return {
        instanceId: parsed.instanceId ?? namespace,
        addresses: parsed.addresses ?? [],
        count: parsed.count ?? parsed.addresses?.length ?? 0,
      };
    } catch {
      // ConfigMap doesn't exist or can't be read — not an error
      return { instanceId: namespace, addresses: [], count: 0 };
    }
  }

  /**
   * Check health by hitting the service with authentication
   */
  private async checkHealth(namespace: string, url: string): Promise<boolean> {
    try {
      const token = await this.getGatewayToken(namespace);
      if (!token) {
        return false;
      }

      return await this.checkHealthWithAuth(url, token);
    } catch {
      return false;
    }
  }

  /**
   * Check health with authentication token
   */
  private async checkHealthWithAuth(
    url: string,
    token: string
  ): Promise<boolean> {
    try {
      // Try a simple OPTIONS request first (doesn't require auth usually)
      const response = await fetch(url, {
        method: 'OPTIONS',
        signal: AbortSignal.timeout(3000),
      });

      // If OPTIONS works, service is up
      if (response.ok) {
        return true;
      }

      // Fallback: try authenticated request to /v1/models
      const authResponse = await fetch(`${url}/v1/models`, {
        signal: AbortSignal.timeout(3000),
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
        },
      });

      return authResponse.ok;
    } catch {
      return false;
    }
  }

  /**
   * Fetch available models from the OpenAI-compatible API
   */
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

  /**
   * Get the tunnel URL from the obol-stack-config ConfigMap in obol-frontend namespace.
   * Written by the CLI via `obol tunnel status`, `obol tunnel login`, or `obol tunnel provision`.
   */
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
   * Get LLM health status by querying the LiteLLM gateway readiness endpoint.
   * Uses /health/readiness which does NOT require API key authentication
   * (unlike /health which requires the master key).
   */
  public async getLLMHealth(): Promise<{
    connected: boolean;
    provider?: string;
    error?: string;
  }> {
    try {
      const response = await fetch(
        'http://litellm.llm.svc.cluster.local:4000/health/readiness',
        { signal: AbortSignal.timeout(3000) }
      );
      if (response.ok) {
        return { connected: true };
      }
      return {
        connected: false,
        error: `LiteLLM returned HTTP ${response.status}`,
      };
    } catch (error) {
      return {
        connected: false,
        error: error instanceof Error ? error.message : 'LiteLLM not reachable',
      };
    }
  }

  /**
   * Get pod status for an openclaw namespace
   */
  private async getPodInfo(
    namespace: string
  ): Promise<{ running: boolean; phase: string; restarts: number }> {
    try {
      const response = await this.coreV1Api.listNamespacedPod({
        namespace,
        labelSelector: 'app.kubernetes.io/name=openclaw',
      });

      if (!response.items || response.items.length === 0) {
        return { running: false, phase: 'NotFound', restarts: 0 };
      }

      const pod = response.items[0];
      const phase = pod.status?.phase || 'Unknown';
      const restarts = pod.status?.containerStatuses?.[0]?.restartCount || 0;

      return { running: phase === 'Running', phase, restarts };
    } catch (error) {
      console.error(`Failed to get pod info for ${namespace}:`, error);
      return { running: false, phase: 'Error', restarts: 0 };
    }
  }
}
