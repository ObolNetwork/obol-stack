import { NextResponse } from 'next/server';
import { PrometheusClient } from '@/lib/services/prometheus';
import { KubernetesAPIClient } from '@/lib/services/kubernetes-api';
import {
  AztecClientMetrics,
  ClientMetricsService,
  ClientType,
  ConsensusClientMetrics,
  ExecutionClientMetrics,
  HeliosClientMetrics,
} from '@/lib/services/client-metrics';
import { config } from '@/lib/config/environment';
import {
  DashboardData,
  LocalNodeData,
  ClientInfo,
  CLIENT_STATUS,
  PORT_STATUS,
  ClientNameType,
} from '@/types/dashboard';
import {
  getClientIcon,
  getNodeModeFromAppInstance,
  parseVersion,
} from '@/lib/utils/client-detection';
import {
  isErpcInstance,
  isEthereumInstance,
} from '@/lib/models/application-instances';
import { resolveMetricsFetchUrl } from '@/lib/utils/cluster-url';

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
    const metricsLive = Boolean(system?.live);
    const nodeOverview = {
      diskUsage:
        system && metricsLive && system.disk.paired
          ? {
              value: Math.round(system.disk.used / 1024 ** 3),
              total: Math.round(system.disk.total / 1024 ** 3),
              percentage: Math.round(system.disk.percentage),
              unit: 'GB',
            }
          : {
              value: 0,
              total: 0,
              percentage: 0,
              unit: 'GB',
              unavailable: true,
            },
      memoryUsage:
        system && metricsLive && system.memory.queried
          ? {
              value: Math.round(system.memory.used / 1024 ** 3),
              total: Math.round(system.memory.total / 1024 ** 3),
              percentage: Math.round(system.memory.percentage),
              unit: 'GB',
            }
          : {
              value: 0,
              total: 0,
              percentage: 0,
              unit: 'GB',
              unavailable: true,
            },
      cpuLoad:
        system && metricsLive && system.cpu.queried
          ? { percentage: Math.round(system.cpu.usage * 10) / 10 }
          : { percentage: 0, unavailable: true },
      uptime: {
        hours:
          system && system.bootTimeAvailable
            ? Math.floor(system.uptime / 3600)
            : 0,
        status: metricsLive ? 'online' : 'offline',
        unavailable: !metricsLive,
        bootUnknown: Boolean(
          metricsLive && system && !system.bootTimeAvailable
        ),
      },
    };

    // Build nodes
    const localNodes: LocalNodeData[] = [];
    let erpcData = null;
    for (const instance of instances) {
      const nodeMode = getNodeModeFromAppInstance(instance.app);

      let blockHeight = 0;
      let finalizedEpoch = 0;
      let participation = 0;
      const clients: ClientInfo[] = [];

      if (isEthereumInstance(instance)) {
        const executionUrl =
          resolveMetricsFetchUrl(
            instance.execution.endpoints.rpc.internal,
            instance.execution.endpoints.rpc.external
          ) ?? instance.execution.endpoints.rpc.internal;
        const executionClient = (await clientMetrics.getMetrics(
          'execution',
          executionUrl
        )) as ExecutionClientMetrics;

        const consensusUrl =
          resolveMetricsFetchUrl(
            instance.consensus.endpoints.rpc.internal,
            instance.consensus.endpoints.rpc.external
          ) ?? instance.consensus.endpoints.rpc.internal;
        const consensusClient = (await clientMetrics.getMetrics(
          'consensus',
          consensusUrl
        )) as ConsensusClientMetrics;

        const executionClientName = instance.execution.client;

        clients.push({
          name: executionClientName as ClientNameType,
          version: parseVersion(executionClient.version),
          peers: executionClient.peers || 0,
          status: !executionClient.syncing
            ? CLIENT_STATUS.SYNCED
            : CLIENT_STATUS.SYNCING,
          icon: getClientIcon(executionClientName as ClientNameType),
          externalUrl: instance.execution.endpoints.rpc.external,
          internalUrl: executionUrl,
        });

        const consensusClientName = instance.consensus.client;

        clients.push({
          name: consensusClientName as ClientNameType,
          version: parseVersion(consensusClient.version),
          peers: consensusClient.peers || 0,
          status: !consensusClient.syncing
            ? CLIENT_STATUS.SYNCED
            : CLIENT_STATUS.SYNCING,
          icon: getClientIcon(consensusClientName as ClientNameType),
          externalUrl: instance.consensus.endpoints.rpc.external,
          internalUrl: consensusUrl,
        });
        blockHeight = executionClient.blockNumber || 0;
        finalizedEpoch = consensusClient.finalizedEpoch || 0;
        participation = consensusClient.participation || 0;
      } else if (isErpcInstance(instance)) {
        erpcData = await clientMetrics.getErpcStatus(config.erpcBaseUrl);
        continue;
      } else {
        const appType = instance.app; // 'helios' | 'aztec'
        const appUrl =
          resolveMetricsFetchUrl(
            instance.endpoints.rpc.internal,
            instance.endpoints.rpc.external
          ) ?? instance.endpoints.rpc.internal;
        const result = (await clientMetrics.getMetrics(
          appType as ClientType,
          appUrl
        )) as HeliosClientMetrics | AztecClientMetrics;
        clients.push({
          name: appType as ClientNameType,
          version: parseVersion(result.version),
          peers: result.peers || 0,
          status: !result.syncing
            ? CLIENT_STATUS.SYNCED
            : CLIENT_STATUS.SYNCING,
          icon: getClientIcon(appType as ClientNameType),
          externalUrl: instance.endpoints.rpc.external,
          internalUrl: appUrl,
        });
        blockHeight = result.blockNumber || 0;
        finalizedEpoch =
          ('finalizedEpoch' in result ? result.finalizedEpoch : 0) || 0;
        participation =
          ('participation' in result ? result.participation : 0) || 0;

        localNodes.push({
          chainName: instance.network,
          app: instance.app,
          appId: instance.id,
          nodeMode,
          status: CLIENT_STATUS.SYNCED,
          blocks: blockHeight,
          finalized: finalizedEpoch,
          participation,
          clients,
          distance: 0,
        });
        continue;
      }

      localNodes.push({
        chainName: instance.network,
        app: instance.app,
        appId: instance.id,
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
      localNodes,
      erpcData,
      nodeOverview,
      ports: portInfo,
    } as DashboardData);
  } catch (error) {
    console.error('Failed to fetch dashboard data:', error);

    return NextResponse.json(
      {
        localNodes: [],
        erpcData: null,
        nodeOverview: {
          diskUsage: {
            value: 0,
            total: 0,
            percentage: 0,
            unit: 'GB',
            unavailable: true,
          },
          memoryUsage: {
            value: 0,
            total: 0,
            percentage: 0,
            unit: 'GB',
            unavailable: true,
          },
          cpuLoad: { percentage: 0, unavailable: true },
          uptime: {
            hours: 0,
            status: 'offline',
            unavailable: true,
            bootUnknown: false,
          },
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
