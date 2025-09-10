/**
 * Kubernetes client for fetching pod and service information
 * Note: This requires the Next.js server to have access to the Kubernetes API
 * In production, this should use proper authentication and service accounts
 */

import { exec } from 'child_process';
import { promisify } from 'util';

const execAsync = promisify(exec);

// Kubernetes API types
interface KubernetesCondition {
  type: string;
  status: string;
}

interface KubernetesContainerStatus {
  restartCount: number;
}

interface KubernetesPod {
  metadata: {
    name: string;
    namespace: string;
    creationTimestamp: string;
  };
  spec: {
    nodeName: string;
  };
  status: {
    phase: string;
    podIP: string;
    conditions?: KubernetesCondition[];
    containerStatuses?: KubernetesContainerStatus[];
  };
}

interface KubernetesPort {
  port: number;
  targetPort: number;
  protocol: string;
}

interface KubernetesService {
  metadata: {
    name: string;
    namespace: string;
  };
  spec: {
    type: string;
    clusterIP: string;
    ports: KubernetesPort[];
  };
  status: {
    loadBalancer?: {
      ingress?: Array<{ ip: string }>;
    };
  };
}

export interface PodInfo {
  name: string;
  namespace: string;
  status: string;
  ready: boolean;
  restarts: number;
  age: string;
  ip?: string;
  node?: string;
}

export interface ServiceInfo {
  name: string;
  namespace: string;
  type: string;
  clusterIP: string;
  externalIP?: string;
  ports: string[];
}

export interface PortStatus {
  port: number;
  name: string;
  open: boolean;
  service?: string;
}

export class KubernetesClient {
  /**
   * Execute kubectl command
   */
  private async kubectl(args: string): Promise<string> {
    try {
      const { stdout } = await execAsync(`kubectl ${args}`);
      return stdout;
    } catch (error: unknown) {
      const errorMessage =
        error instanceof Error ? error.message : 'Unknown error';
      console.error(`kubectl command failed: ${errorMessage}`);
      throw error;
    }
  }

  /**
   * Get pods in a namespace
   */
  async getPods(namespace: string = 'l1'): Promise<PodInfo[]> {
    try {
      const output = await this.kubectl(`get pods -n ${namespace} -o json`);
      const data = JSON.parse(output);

      return data.items.map((pod: KubernetesPod) => ({
        name: pod.metadata.name,
        namespace: pod.metadata.namespace,
        status: pod.status.phase,
        ready:
          pod.status.conditions?.find(
            (c: KubernetesCondition) => c.type === 'Ready'
          )?.status === 'True',
        restarts:
          pod.status.containerStatuses?.reduce(
            (sum: number, c: KubernetesContainerStatus) => sum + c.restartCount,
            0
          ) || 0,
        age: this.calculateAge(pod.metadata.creationTimestamp),
        ip: pod.status.podIP,
        node: pod.spec.nodeName,
      }));
    } catch (error) {
      console.error(`Failed to fetch pods in namespace ${namespace}:`, error);
      return [];
    }
  }

  /**
   * Get services in a namespace
   */
  async getServices(namespace: string = 'l1'): Promise<ServiceInfo[]> {
    try {
      const output = await this.kubectl(`get services -n ${namespace} -o json`);
      const data = JSON.parse(output);

      return data.items.map((service: KubernetesService) => ({
        name: service.metadata.name,
        namespace: service.metadata.namespace,
        type: service.spec.type,
        clusterIP: service.spec.clusterIP,
        externalIP: service.status.loadBalancer?.ingress?.[0]?.ip,
        ports: service.spec.ports.map(
          (p: KubernetesPort) => `${p.port}/${p.protocol}`
        ),
      }));
    } catch (error) {
      console.error(
        `Failed to fetch services in namespace ${namespace}:`,
        error
      );
      return [];
    }
  }

  /**
   * Get client information (Nethermind and Nimbus)
   */
  async getClientInfo() {
    try {
      const pods = await this.getPods('l1');

      const executionPod = pods.find(p => p.name.includes('execution'));
      const beaconPod = pods.find(p => p.name.includes('beacon'));

      // Get version info from pod labels or annotations (simplified)
      // In a real implementation, we'd query the actual client APIs
      return {
        execution: {
          name: 'Nethermind',
          version: 'v1.32.4', // This should be fetched from the actual client
          status: executionPod?.ready ? 'synced' : 'syncing',
          peers: 0, // This needs to be fetched from the client API
        },
        consensus: {
          name: 'Nimbus',
          version: 'v25.6.0', // This should be fetched from the actual client
          status: beaconPod?.ready ? 'synced' : 'syncing',
          peers: 0, // This needs to be fetched from the client API
        },
      };
    } catch (error) {
      console.error('Failed to fetch client info:', error);
      throw error;
    }
  }

  /**
   * Check port status
   */
  async checkPortStatus(): Promise<PortStatus[]> {
    const commonPorts = [
      { port: 30303, name: 'P2P' },
      { port: 8545, name: 'JSON-RPC' },
      { port: 9090, name: 'Prometheus' },
      { port: 4000, name: 'Beacon API' },
      { port: 3000, name: 'Frontend' },
      { port: 80, name: 'HTTP' },
      { port: 443, name: 'HTTPS' },
    ];

    try {
      // Get all services to check for port availability
      const allServices = await this.kubectl(
        'get services --all-namespaces -o json'
      );
      const servicesData = JSON.parse(allServices);

      return commonPorts.map(p => {
        // Check if any service exposes this port
        const service = servicesData.items.find((s: KubernetesService) =>
          s.spec.ports?.some(
            (sp: KubernetesPort) =>
              sp.port === p.port || sp.targetPort === p.port
          )
        );

        return {
          port: p.port,
          name: p.name,
          open: !!service,
          service: service?.metadata?.name,
        };
      });
    } catch (error) {
      console.error('Failed to check port status:', error);
      // Return default closed status for all ports
      return commonPorts.map(p => ({
        ...p,
        open: false,
      }));
    }
  }

  /**
   * Calculate age from timestamp
   */
  private calculateAge(timestamp: string): string {
    const created = new Date(timestamp);
    const now = new Date();
    const diff = now.getTime() - created.getTime();

    const seconds = Math.floor(diff / 1000);
    const minutes = Math.floor(seconds / 60);
    const hours = Math.floor(minutes / 60);
    const days = Math.floor(hours / 24);

    if (days > 0) return `${days}d`;
    if (hours > 0) return `${hours}h`;
    if (minutes > 0) return `${minutes}m`;
    return `${seconds}s`;
  }

  /**
   * Get pod logs
   */
  async getPodLogs(
    namespace: string,
    podName: string,
    lines: number = 100
  ): Promise<string> {
    try {
      const output = await this.kubectl(
        `logs -n ${namespace} ${podName} --tail=${lines}`
      );
      return output;
    } catch (error) {
      console.error(`Failed to fetch logs for pod ${podName}:`, error);
      return '';
    }
  }
}
