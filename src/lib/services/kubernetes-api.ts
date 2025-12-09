/**
 * Kubernetes API client for use within the cluster
 * Uses the Kubernetes API directly via service account token
 */

import { ObjectCoreV1Api } from '@kubernetes/client-node/dist/gen/types/ObjectParamAPI';
import { NodeModeType } from '../../types/dashboard';
import { CoreV1Api, KubeConfig } from '@kubernetes/client-node';
import * as fs from 'fs';
import {
  AnyApplicationInstance,
  BaseApplicationInstance,
  isValidApp,
  validateApplicationInstance,
} from '../models/application-instances';

export interface ServiceModeInfo {
  url: string;
  type: NodeModeType;
}

export class KubernetesAPIClient {
  private baseUrl: string;
  private token?: string;
  // private kc: KubeConfig;
  private coreV1Api: ObjectCoreV1Api;

  constructor() {
    // When running in a pod, Kubernetes automatically mounts the service account
    // The API server is always available at this address from within the cluster
    this.baseUrl = 'https://kubernetes.default.svc';

    const kc = this.getKubeConfig();
    this.coreV1Api = kc.makeApiClient(CoreV1Api);
    // In production, read the service account token
    // For now, we'll use a simpler approach with direct service URLs
  }

  private getKubeConfig(): KubeConfig {
    const kc = new KubeConfig();
    if (process.env.KUBERNETES_SERVICE_HOST) {
      // In-cluster mode: Use service account token
      console.log('[K8s] Using in-cluster configuration');
      kc.loadFromCluster();
    } else {
      // Local development: Require NEXT_PUBLIC_KUBECONFIG env var
      if (!process.env.NEXT_PUBLIC_KUBECONFIG) {
        throw new Error(
          'NEXT_PUBLIC_KUBECONFIG environment variable is required for local development. ' +
            'Set it to the path of your kubeconfig file.'
        );
      }

      // Check if file exists
      if (!fs.existsSync(process.env.NEXT_PUBLIC_KUBECONFIG)) {
        throw new Error(
          `Kubeconfig file not found at: ${process.env.NEXT_PUBLIC_KUBECONFIG}`
        );
      }

      try {
        kc.loadFromFile(process.env.NEXT_PUBLIC_KUBECONFIG);
        console.log(`[K8s] Context: ${kc.getCurrentContext()}`);
      } catch (error) {
        throw new Error(
          `Failed to load kubeconfig from ${process.env.NEXT_PUBLIC_KUBECONFIG}: ${error instanceof Error ? error.message : 'Unknown error'}`
        );
      }
    }

    return kc;
  }

  async getApplicationInstances(): Promise<AnyApplicationInstance[]> {
    const response = await this.coreV1Api.listConfigMapForAllNamespaces({
      labelSelector: 'app.kubernetes.io/part-of=obol.stack',
    });

    const instances: AnyApplicationInstance[] = [];

    for (const configMap of response.items) {
      const labels = configMap.metadata?.labels;
      const app = labels?.['obol.stack/app'];
      const id = labels?.['obol.stack/id'];
      const data = configMap.data?.['metadata.json'];
      const creationTimestamp = configMap.metadata?.creationTimestamp;

      if (app && id && data && creationTimestamp) {
        // Only process if app is a valid app type
        if (!isValidApp(app)) {
          console.warn(`Unknown app type: ${app}, skipping instance ${id}`);
          continue;
        }

        try {
          const parsed = JSON.parse(data);

          const base: BaseApplicationInstance = {
            app, // TypeScript now knows app is 'helios' | 'aztec' | 'ethereum'
            id,
            creationTimestamp: new Date(creationTimestamp),
          };

          const validated = validateApplicationInstance(app, parsed, base);
          instances.push(validated);
        } catch (error) {
          console.error(
            `Failed to parse/validate metadata.json for ${app}/${id}:`,
            error
          );
        }
      }
    }

    return instances;
  }

  /**
   * Check service health by attempting to connect to them
   */
  async checkServiceHealth(serviceUrl: string): Promise<boolean> {
    try {
      // Check if this is an Aztec node by URL pattern
      const isAztecNode =
        serviceUrl.includes('aztec') || serviceUrl.includes('l2-');

      // Use appropriate health check method
      const method = isAztecNode ? 'node_getL2Tips' : 'web3_clientVersion';

      const response = await fetch(serviceUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          method: method,
          params: [],
          id: 1,
        }),
        signal: AbortSignal.timeout(5000), // 5 second timeout
      });
      return response.ok;
    } catch {
      return false;
    }
  }

  // async getAvailableModes(): Promise<ServiceModeInfo[]> {
  //   const modes = [
  //     {
  //       url: config.fullNodeExecutionUrl,
  //       type: NODE_MODES.FULL_MODE,
  //     },
  //     {
  //       url: config.heliosClientUrl,
  //       type: NODE_MODES.LIGHT_MODE,
  //     },
  //     {
  //       url: config.aztecFullNodeUrl,
  //       type: NODE_MODES.AZTEC_FULLNODE,
  //     },
  //     {
  //       url: config.aztecSequencerUrl,
  //       type: NODE_MODES.AZTEC_SEQUENCER,
  //     },
  //     {
  //       url: config.aztecProverNodeUrl,
  //       type: NODE_MODES.AZTEC_PROVER,
  //     },
  //   ];

  //   // Check all modes in parallel
  //   const results = await Promise.allSettled(
  //     modes.map(service => this.checkServiceHealth(service.url))
  //   );

  //   // Return only the modes that are available
  //   const availableModes = modes.filter((service, index) => {
  //     const result = results[index];
  //     const isAvailable =
  //       result.status === 'fulfilled' && result.value === true;
  //     return isAvailable;
  //   });

  //   return availableModes;
  // }

  /**
   * Check port availability based on known services
   */
  async checkPorts() {
    const ports = [
      {
        port: 30303,
        name: 'P2P',
        service: 'l1-full-node-execution',
        check: false,
      },
      {
        port: 8545,
        name: 'JSON-RPC',
        service: 'l1-full-node-execution',
        check: true,
      },
      {
        port: 9090,
        name: 'Prometheus',
        service: 'monitoring-kube-prometheus-prometheus',
        check: true,
      },
      {
        port: 5052,
        name: 'Beacon API',
        service: 'l1-full-node-beacon',
        check: true,
      },
      {
        port: 80,
        name: 'HTTP',
        service: 'ingress-nginx-controller',
        check: false,
      },
      {
        port: 443,
        name: 'HTTPS',
        service: 'ingress-nginx-controller',
        check: false,
      },
    ];

    const results = await Promise.all(
      ports.map(async p => {
        let open = false;

        if (p.check) {
          // For services we can check, try to connect
          const url = p.service.includes('prometheus')
            ? `http://${p.service}.monitoring.svc.cluster.local:${p.port}/-/healthy`
            : p.service.includes('beacon')
              ? `http://${p.service}.l1.svc.cluster.local:${p.port}/eth/v1/node/health`
              : `http://${p.service}.l1.svc.cluster.local:${p.port}`;

          open = await this.checkServiceHealth(url);
        } else {
          // For P2P and ingress ports, assume they're open if the service exists
          open = true;
        }

        return {
          port: p.port,
          name: p.name,
          open,
          service: p.service,
        };
      })
    );

    return results;
  }
}
