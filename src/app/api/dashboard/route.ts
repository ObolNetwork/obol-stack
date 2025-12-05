import { NextResponse } from 'next/server';
import { PrometheusClient } from '@/lib/services/prometheus';
import { KubernetesAPIClient } from '@/lib/services/kubernetes-api';
import {
  ClientMetricsService,
  ClientType,
} from '@/lib/services/client-metrics';
import { config } from '@/lib/config/environment';
import {
  DashboardData,
  ChainData,
  ClientInfo,
  CLIENT_STATUS,
  PORT_STATUS,
} from '@/types/dashboard';
import {
  getClientInfoByAppType,
  getNodeModeFromAppInstance,
  parseVersion,
} from '@/lib/utils/client-detection';
import { getChainInfoFromName } from '@/lib/utils/chain-mapping';
import { isEthereumInstance } from '@/lib/models/application-instances';

async function fetchClientMetrics(
  clientType: ClientType,
  endpoint: string,
  clientMetrics: ClientMetricsService
): Promise<ClientInfo> {
  const result = await clientMetrics.getMetrics(clientType, endpoint);
  const client = getClientInfoByAppType(clientType);
  return {
    name: client.name,
    version: parseVersion(result.version),
    peers: result.peers || 0,
    status: !result.syncing ? CLIENT_STATUS.SYNCED : CLIENT_STATUS.SYNCING,
    icon: client.icon,
  };
}

export async function GET() {
  try {
    const prometheus = new PrometheusClient(config.prometheusUrl);
    const k8s = new KubernetesAPIClient();
    const clientMetrics = new ClientMetricsService();

    const [systemMetrics, portStatus, appInstances] = await Promise.allSettled([
      prometheus.getSystemMetrics(),
      k8s.checkPorts(),
      k8s.getApplicationInstances(),
    ]);

    const instances =
      appInstances.status === 'fulfilled' ? appInstances.value : [];

    // Node overview
    const system =
      systemMetrics.status === 'fulfilled' ? systemMetrics.value : null;
    const nodeOverview = {
      diskUsage: system
        ? {
            value: Math.round(system.disk.used / 1024 ** 3),
            total: Math.round(system.disk.total / 1024 ** 3),
            percentage: Math.round(system.disk.percentage),
            unit: 'GB',
          }
        : { value: 0, total: 0, percentage: 0, unit: 'GB' },
      memoryUsage: system
        ? {
            value: Math.round(system.memory.used / 1024 ** 3),
            total: Math.round(system.memory.total / 1024 ** 3),
            percentage: Math.round(system.memory.percentage),
            unit: 'GB',
          }
        : { value: 0, total: 0, percentage: 0, unit: 'GB' },
      cpuLoad: { percentage: system ? Math.round(system.cpu.usage) : 0 },
      uptime: {
        hours: system ? Math.floor(system.uptime / 3600) : 0,
        status: system ? 'online' : 'offline',
      },
    };

    // Build chains
    const chains: ChainData[] = [];
    for (const instance of instances) {
      const chainInfo = getChainInfoFromName(instance.network);
      if (!chainInfo) continue;
      const nodeMode = getNodeModeFromAppInstance(instance.app);

      let blockHeight = 0;
      let finalizedEpoch = 0;
      let participation = 0;
      const clients: ClientInfo[] = [];

      if (isEthereumInstance(instance)) {
        const [executionClient, consensusClient] = await Promise.all([
          fetchClientMetrics(
            'execution',
            instance.execution.endpoints.rpc.external,
            clientMetrics
          ),
          fetchClientMetrics(
            'consensus',
            instance.consensus.endpoints.rpc.external,
            clientMetrics
          ),
        ]);
        clients.push(executionClient, consensusClient);
        blockHeight = instance.execution ? executionClient.peers : 0; // Example, replace with actual metric
        finalizedEpoch = instance.consensus ? consensusClient.peers : 0; // Example
        participation = instance.consensus ? consensusClient.peers : 0; // Example
      } else {
        const appType = instance.app; // 'helios' | 'aztec'
        const client = await fetchClientMetrics(
          appType as ClientType,
          instance.endpoints.rpc.internal,
          clientMetrics
        );
        clients.push(client);
        blockHeight = client.peers; // Example, replace with actual metric
        finalizedEpoch = client.peers;
        participation = client.peers;
      }

      chains.push({
        chainName: chainInfo.chainName,
        network: chainInfo.network,
        nodeMode,
        status: CLIENT_STATUS.SYNCED,
        blocks: blockHeight,
        finalized: finalizedEpoch,
        participation,
        clients,
        distance: 0,
      });
    }

    // Port info
    const ports = portStatus.status === 'fulfilled' ? portStatus.value : [];
    const portInfo = {
      p2p: {
        port: 30303,
        status: ports.find(p => p.port === 30303)?.open
          ? PORT_STATUS.OPEN
          : PORT_STATUS.CLOSED,
      },
      jsonRpc: {
        port: 8545,
        status: ports.find(p => p.port === 8545)?.open
          ? PORT_STATUS.OPEN
          : PORT_STATUS.CLOSED,
      },
      prometheus: {
        port: 9090,
        status: ports.find(p => p.port === 9090)?.open
          ? PORT_STATUS.OPEN
          : PORT_STATUS.CLOSED,
      },
      beaconApi: {
        port: 5052,
        status: ports.find(p => p.port === 5052)?.open
          ? PORT_STATUS.OPEN
          : PORT_STATUS.CLOSED,
      },
    };

    return NextResponse.json({
      chains,
      nodeOverview,
      ports: portInfo,
    } as DashboardData);
  } catch (error) {
    console.error('Failed to fetch dashboard data:', error);

    return NextResponse.json(
      {
        chains: [],
        nodeOverview: {
          diskUsage: { value: 0, total: 0, percentage: 0, unit: 'GB' },
          memoryUsage: { value: 0, total: 0, percentage: 0, unit: 'GB' },
          cpuLoad: { percentage: 0 },
          uptime: { hours: 0, status: 'offline' },
        },
        ports: {
          p2p: { port: 30303, status: 'closed' },
          jsonRpc: { port: 8545, status: 'closed' },
          prometheus: { port: 9090, status: 'closed' },
          beaconApi: { port: 5052, status: 'closed' },
        },
      } as DashboardData,
      { status: 503 }
    );
  }
}
