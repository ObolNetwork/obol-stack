import { NextResponse } from 'next/server';
import { PrometheusClient } from '@/lib/services/prometheus';
import { KubernetesAPIClient } from '@/lib/services/kubernetes-api';
import {
  ClientMetricsService,
  ConsensusClientMetrics,
  ExecutionClientMetrics,
  HeliosClientMetrics,
} from '@/lib/services/client-metrics';
import { config } from '@/lib/config/environment';
import {
  DashboardData,
  ChainData,
  ClientInfo,
  CLIENT_STATUS,
  PORT_STATUS,
  NETWORKS,
  CHAINS,
} from '@/types/dashboard';
import { getClients, parseVersion } from '@/lib/utils/client-detection';
import { ChainInfo, getChainInfoFromId } from '@/lib/utils/chain-mapping';

export async function GET() {
  try {
    // Initialize service clients
    const prometheus = new PrometheusClient(config.prometheusUrl);
    const k8s = new KubernetesAPIClient();
    const clientMetrics = new ClientMetricsService();

    // Fetch all metrics in parallel
    const [
      systemMetrics,
      // ethereumMetrics, // Commented out - eRPC doesn't recognize 'hoodi'
      availableModes,
      portStatus,
    ] = await Promise.allSettled([
      prometheus.getSystemMetrics(),
      k8s.getAvailableModes(),
      // erpc.getNetworkMetrics('hoodi'), // We get this data from ClientMetricsService instead
      k8s.checkPorts(),
    ]);

    // Process system metrics
    const system =
      systemMetrics.status === 'fulfilled' ? systemMetrics.value : null;
    const nodeOverview = {
      diskUsage: system
        ? {
            value: Math.round(system.disk.used / (1024 * 1024 * 1024)),
            total: Math.round(system.disk.total / (1024 * 1024 * 1024)),
            percentage: Math.round(system.disk.percentage),
            unit: 'GB',
          }
        : {
            value: 0,
            total: 0,
            percentage: 0,
            unit: 'GB',
          },
      memoryUsage: system
        ? {
            value: Math.round(system.memory.used / (1024 * 1024 * 1024)),
            total: Math.round(system.memory.total / (1024 * 1024 * 1024)),
            percentage: Math.round(system.memory.percentage),
            unit: 'GB',
          }
        : {
            value: 0,
            total: 0,
            percentage: 0,
            unit: 'GB',
          },
      cpuLoad: {
        percentage: system ? Math.round(system.cpu.usage) : 19,
      },
      uptime: {
        hours: system ? Math.floor(system.uptime / 3600) : 76,
        status: 'online',
      },
    };

    // Build chains array
    const chains: ChainData[] = [];

    if (availableModes.status === 'fulfilled') {
      for (const mode of availableModes.value) {
        let blockHeight = 0;
        let finalizedEpoch = 0;
        let chainInfo: ChainInfo = {
          chainName: CHAINS.ETHEREUM,
          network: NETWORKS.MAINNET,
        };
        let participation = 0;
        const chainClients = getClients(mode.type);

        const clients: ClientInfo[] = [];

        for (const client of chainClients) {
          const result = await clientMetrics.getMetrics(
            client.type,
            client.endpoint,
            client.beaconUrl
          );

          if (client.type === 'execution') {
            blockHeight = (result as ExecutionClientMetrics).blockNumber || 0;
            chainInfo = getChainInfoFromId(
              (result as ExecutionClientMetrics).chainId
            );
          } else if (client.type === 'helios') {
            blockHeight = (result as HeliosClientMetrics).blockNumber || 0;
            chainInfo = getChainInfoFromId(
              (result as HeliosClientMetrics).chainId
            );
            finalizedEpoch =
              (result as HeliosClientMetrics).finalizedEpoch || 0;
            participation = (result as HeliosClientMetrics).participation || 0;
          } else if (client.type === 'consensus') {
            finalizedEpoch =
              (result as ConsensusClientMetrics).finalizedEpoch || 0;
            participation =
              (result as ConsensusClientMetrics).participation || 0;
          }
          clients.push({
            name: client.name,
            version: parseVersion(result.version),
            peers: result.peers || 0,
            status: !result.syncing
              ? CLIENT_STATUS.SYNCED
              : CLIENT_STATUS.SYNCING,
            icon: client.icon,
          });
        }
        chains.push({
          chainName: chainInfo.chainName,
          network: chainInfo.network,
          nodeMode: mode.type,
          status: CLIENT_STATUS.SYNCED,
          blocks: blockHeight,
          finalized: finalizedEpoch,
          participation: participation,
          clients: clients,
          distance: 0,
        });
      }
    }

    // Process port status
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

    // Construct the dashboard response
    const dashboardData: DashboardData = {
      chains,
      nodeOverview,
      ports: portInfo,
    };

    return NextResponse.json(dashboardData);
  } catch (error) {
    console.error('Failed to fetch dashboard data:', error);

    // Return empty data structure - frontend will show N/A
    const emptyData: DashboardData = {
      chains: [],
      nodeOverview: {
        diskUsage: {
          value: 0,
          total: 0,
          percentage: 0,
          unit: 'GB',
        },
        memoryUsage: {
          value: 0,
          total: 0,
          percentage: 0,
          unit: 'GB',
        },
        cpuLoad: {
          percentage: 0,
        },
        uptime: {
          hours: 0,
          status: 'offline',
        },
      },
      ports: {
        p2p: { port: 30303, status: 'closed' },
        jsonRpc: { port: 8545, status: 'closed' },
        prometheus: { port: 9090, status: 'closed' },
        beaconApi: { port: 5052, status: 'closed' },
      },
    };

    return NextResponse.json(emptyData, { status: 503 });
  }
}
