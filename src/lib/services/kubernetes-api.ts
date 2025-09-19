/**
 * Kubernetes API client for use within the cluster
 * Uses the Kubernetes API directly via service account token
 */

import {
  CHAINS,
  ClientInfo,
  NETWORKS,
  NetworkType,
  NODE_MODES,
  NodeModeType,
} from '../../types/dashboard';
import { detectClient, parseVersion } from '../utils/client-detection';

export interface PodInfo {
  name: string;
  namespace: string;
  status: string;
  ready: boolean;
  restarts: number;
  createdAt: string;
  ip?: string;
  node?: string;
}

export interface ServiceInfo {
  name: string;
  namespace: string;
  type: string;
  clusterIP: string;
  ports: Array<{
    port: number;
    targetPort: number;
    protocol: string;
  }>;
}

export interface ServiceModeInfo {
  url: string;
  type: NodeModeType;
}

export class KubernetesAPIClient {
  private baseUrl: string;
  private token?: string;

  constructor() {
    // When running in a pod, Kubernetes automatically mounts the service account
    // The API server is always available at this address from within the cluster
    this.baseUrl = 'https://kubernetes.default.svc';

    // In production, read the service account token
    // For now, we'll use a simpler approach with direct service URLs
  }

  /**
   * Check service health by attempting to connect to them
   */
  async checkServiceHealth(serviceUrl: string): Promise<boolean> {
    try {
      const response = await fetch(serviceUrl, {
        method: 'GET',
        signal: AbortSignal.timeout(5000), // 5 second timeout
      });
      return response.ok;
    } catch {
      return false;
    }
  }

  async getAvailableModes(): Promise<ServiceModeInfo[]> {
    const modes = [
      {
        url: 'http://l1-full-node-execution.l1.svc.cluster.local:8545',
        type: NODE_MODES.FULL_MODE,
      },
      {
        url: 'http://l1-light-client-helios.l1.svc.cluster.local:8545',
        type: NODE_MODES.LIGHT_MODE,
      },
    ];

    // Check all modes in parallel
    const results = await Promise.allSettled(
      modes.map(service => this.checkServiceHealth(service.url))
    );

    // Return only the modes that are available
    const availableModes = modes.filter((service, index) => {
      const result = results[index];
      const isAvailable =
        result.status === 'fulfilled' && result.value === true;
      return isAvailable;
    });

    return availableModes;
  }

  /**
   * Get client versions by querying the actual services
   * Supports both full mode (Nethermind + Nimbus) and light mode (Helios)
   */
  async getClients(mode: NodeModeType): Promise<ClientInfo[]> {
    const clients: ClientInfo[] = [];

    if (mode === NODE_MODES.FULL_MODE) {
      // Full mode: check both Nethermind and Nimbus
      const [nethermind, nimbus] = await Promise.allSettled([
        // Nethermind
        fetch('http://l1-full-node-execution.l1.svc.cluster.local:8545', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            jsonrpc: '2.0',
            method: 'web3_clientVersion',
            params: [],
            id: 1,
          }),
          signal: AbortSignal.timeout(5000),
        }),

        // Nimbus
        fetch(
          'http://l1-full-node-beacon.l1.svc.cluster.local:5052/eth/v1/node/version',
          {
            headers: { Accept: 'application/json' },
            signal: AbortSignal.timeout(5000),
          }
        ),
      ]);

      // Process Nethermind result
      if (nethermind.status === 'fulfilled' && nethermind.value.ok) {
        const data = await nethermind.value.json();
        if (data.result) {
          const client = detectClient(data.result);
          clients.push({
            name: client.name,
            version: parseVersion(data.result),
            peers: 0, // TODO: Get actual peer count
            status: 'synced', // TODO: Get actual sync status
            icon: client.iconUrl,
          });
        }
      }

      // Process Nimbus result
      if (nimbus.status === 'fulfilled' && nimbus.value.ok) {
        const data = await nimbus.value.json();
        if (data.data?.version) {
          const client = detectClient(data.data.version);
          clients.push({
            name: client.name,
            version: parseVersion(data.data.version),
            peers: 0, // TODO: Get actual peer count
            status: 'synced', // TODO: Get actual sync status
            icon: client.iconUrl,
          });
        }
      }
    } else if (mode === NODE_MODES.LIGHT_MODE) {
      // Light mode: check Helios using the service URL
      const helios = await fetch(
        'http://l1-light-client-helios.l1.svc.cluster.local:8545',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            jsonrpc: '2.0',
            method: 'web3_clientVersion',
            params: [],
            id: 1,
          }),
          signal: AbortSignal.timeout(5000),
        }
      );

      if (helios.ok) {
        const data = await helios.json();
        if (data.result) {
          const client = detectClient(data.result);
          clients.push({
            name: client.name,
            version: parseVersion(data.result),
            peers: 0, // TODO: Get actual peer count
            status: 'synced', // TODO: Get actual sync status
            icon: client.iconUrl,
          });
        }
      }
    }

    return clients;
  }

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
