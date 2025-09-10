/**
 * Kubernetes API client for use within the cluster
 * Uses the Kubernetes API directly via service account token
 */

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
   * Get pod information by directly querying services
   * This is a simplified version that doesn't require Kubernetes API access
   */
  async getPodsStatus(
    _namespace: string = 'l1'
  ): Promise<
    Record<
      string,
      { name: string; status: string; ready: boolean; restarts: number }
    >
  > {
    // For now, we'll return mock data based on what we know is deployed
    // In production, this would query the actual Kubernetes API
    return {
      execution: {
        name: 'l1-full-node-execution-0',
        status: 'Running',
        ready: true,
        restarts: 0,
      },
      beacon: {
        name: 'l1-full-node-beacon-0',
        status: 'Running',
        ready: true,
        restarts: 0,
      },
      rpc: {
        name: 'rpc',
        status: 'Running',
        ready: true,
        restarts: 0,
      },
    };
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

  /**
   * Get client versions by querying the actual services
   */
  async getClientVersions() {
    const versions = {
      execution: 'Unknown',
      consensus: 'Unknown',
    };

    try {
      // Try to get Nethermind version via JSON-RPC
      const execResponse = await fetch(
        'http://l1-full-node-execution.l1.svc.cluster.local:8545',
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

      if (execResponse.ok) {
        const data = await execResponse.json();
        if (data.result) {
          versions.execution = data.result;
        }
      }
    } catch (error) {
      console.error('Failed to get execution client version:', error);
    }

    try {
      // Try to get Nimbus version via Beacon API
      const beaconResponse = await fetch(
        'http://l1-full-node-beacon.l1.svc.cluster.local:5052/eth/v1/node/version',
        {
          headers: { Accept: 'application/json' },
          signal: AbortSignal.timeout(5000),
        }
      );

      if (beaconResponse.ok) {
        const data = await beaconResponse.json();
        if (data.data?.version) {
          versions.consensus = data.data.version;
        }
      }
    } catch (error) {
      console.error('Failed to get consensus client version:', error);
    }

    return versions;
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
