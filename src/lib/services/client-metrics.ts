/**
 * Service for fetching real-time metrics from Ethereum and Aztec clients
 */

import type {
  ErpcStatusData,
  ErpcUpstream,
  ErpcUpstreamMetrics,
} from '@/types/dashboard';

export type ClientType =
  | 'execution'
  | 'consensus'
  | 'helios'
  | 'aztec'
  | 'erpc';

export interface ExecutionClientMetrics {
  peers: number;
  version: string;
  syncing: boolean;
  blockNumber: number;
  chainId: number;
  finalizedEpoch?: number; // Helios enrichment
  participation?: number; // Helios enrichment
}

export interface HeliosClientMetrics {
  peers: number; // Helios doesn't expose peers, usually 0
  version: string;
  syncing: boolean; // computed from beacon data
  blockNumber: number;
  chainId: number;
  finalizedEpoch: number;
  participation: number;
}

export interface ConsensusClientMetrics {
  peers: number;
  version: string;
  syncing: boolean;
  headSlot: number;
  finalizedEpoch: number;
  participation?: number; // Nimbus-specific enrichment
}

export interface ErpcClientMetrics {
  peers: number;
  version: string;
  syncing: boolean;
  blockNumber: number;
  chainId: number;
  upstreamCount: number;
}

export interface AztecClientMetrics {
  peers: number;
  version: string;
  syncing: boolean;
  blockNumber: number;
  chainId: number;
  finalizedEpoch: number; // proven block number
  participation: number; // proof progress percentage
  latestBlock?: number;
  provenBlock?: number;
  finalizedBlock?: number;
}

export class ClientMetricsService {
  // Cache for Nimbus-specific participation data (endpoint is non-standard,
  // only Nimbus supports it). Keyed by beacon URL, cached for 5 minutes to
  // avoid spamming the beacon client on every dashboard poll.
  private static participationCache = new Map<
    string,
    { value: number; expiresAt: number }
  >();
  private static readonly PARTICIPATION_TTL_MS = 60 * 1000;

  constructor() {}

  /**
   * Fetch the full eRPC status from GET / on the eRPC server.
   * baseUrl should be the origin of the eRPC service (e.g. http://erpc.erpc.svc.cluster.local)
   */
  async getErpcStatus(baseUrl: string): Promise<ErpcStatusData> {
    try {
      const origin = new URL(baseUrl).origin;
      const resp = await fetch(`${origin}/`, {
        signal: AbortSignal.timeout(5000),
      });
      const data = await resp.json();

      const networks: ErpcStatusData['networks'] = (data?.rpc ?? []).map(
        (n: { id: string; alias: string; state: string }) => ({
          id: n.id,
          alias: n.alias,
          state: n.state,
        })
      );

      // Parse upstream metrics when available (data.details.rpc.upstreams)
      const rawUpstreams: Record<
        string,
        { network: string; metrics: ErpcUpstreamMetrics }
      > = data?.details?.rpc?.upstreams ?? {};

      const upstreams: ErpcUpstream[] = Object.entries(rawUpstreams).map(
        ([id, upstream]) => ({
          id,
          network: upstream.network || '',
          metrics: upstream.metrics,
        })
      );

      const networkCount =
        data?.details?.rpc?.config?.networks ?? networks.length;
      const upstreamCount =
        data?.details?.rpc?.config?.upstreams ?? upstreams.length;

      return {
        networks,
        status: data?.status ?? undefined,
        message: data?.message ?? undefined,
        networkCount,
        upstreamCount,
        upstreams: upstreams.length > 0 ? upstreams : undefined,
      };
    } catch {
      return { networks: [] };
    }
  }

  /**
   * Unified method to fetch client metrics
   * @param type - 'execution' | 'consensus' | 'helios'
   * @param url - The client endpoint URL
   */
  async getMetrics(
    type: ClientType,
    url: string
  ): Promise<
    | ExecutionClientMetrics
    | ConsensusClientMetrics
    | HeliosClientMetrics
    | AztecClientMetrics
    | ErpcClientMetrics
  > {
    switch (type) {
      case 'execution':
        return this.getExecutionMetrics(url);
      case 'helios':
        return this.getHeliosMetrics(url);
      case 'consensus':
        return this.getConsensusMetrics(url);
      case 'aztec':
        return this.getAztecMetrics(url);
      case 'erpc':
        return this.getErpcMetrics(url);
      default:
        throw new Error(`Unknown client type: ${type}`);
    }
  }

  /**
   * Fetch metrics from full EL clients (Nethermind, Geth, etc.)
   */
  private async getExecutionMetrics(
    executionUrl: string
  ): Promise<ExecutionClientMetrics> {
    try {
      const version = await this.fetchWeb3ClientVersion(executionUrl);
      const peerCount = await this.fetchNetPeerCount(executionUrl);
      const blockNumber = await this.fetchBlockNumber(executionUrl);
      const chainId = await this.fetchChainId(executionUrl);
      const syncing = await this.fetchIsSyncing(executionUrl);

      return { peers: peerCount, version, syncing, blockNumber, chainId };
    } catch (err) {
      console.error('Failed to fetch execution metrics:', err);
      return {
        peers: 0,
        version: 'Unknown',
        syncing: false,
        blockNumber: 0,
        chainId: 1,
      };
    }
  }

  /**
   * Fetch metrics from Helios light execution client
   */
  private async getHeliosMetrics(
    executionUrl: string
  ): Promise<HeliosClientMetrics> {
    try {
      const version = await this.fetchWeb3ClientVersion(executionUrl);
      const blockNumber = await this.fetchBlockNumber(executionUrl);
      const chainId = await this.fetchChainId(executionUrl);

      // For Helios, we consider syncing false (or you could compute based on blockNumber vs beacon head)
      const syncing = false;

      return {
        peers: 0,
        version,
        syncing,
        blockNumber,
        chainId,
        finalizedEpoch: 0,
        participation: 0,
      };
    } catch (err) {
      console.error('Failed to fetch Helios metrics:', err);
      return {
        peers: 0,
        version: 'Unknown',
        syncing: false,
        blockNumber: 0,
        chainId: 1,
        finalizedEpoch: 0,
        participation: 0,
      };
    }
  }

  /**
   * Fetch metrics from Nimbus or other consensus clients
   */
  private async getConsensusMetrics(
    consensusUrl: string
  ): Promise<ConsensusClientMetrics> {
    try {
      const version = await this.fetchConsensusVersion(consensusUrl);
      const peers = await this.fetchConsensusPeers(consensusUrl);
      const syncing = await this.fetchConsensusSyncing(consensusUrl);
      const headSlot = await this.fetchConsensusHeadSlot(consensusUrl);
      const finalizedEpoch =
        await this.fetchFinalizedEpochFromBeacon(consensusUrl);

      // /eth/v1/validator/participation is Nimbus-specific — skip for other clients.
      const isNimbus = version.toLowerCase().includes('nimbus');
      const participation = isNimbus
        ? await this.fetchParticipationCached(consensusUrl)
        : undefined;

      return {
        peers,
        version,
        syncing,
        headSlot,
        finalizedEpoch,
        participation,
      };
    } catch (err) {
      console.error('Failed to fetch consensus metrics:', err);
      return {
        peers: 0,
        version: 'Unknown',
        syncing: false,
        headSlot: 0,
        finalizedEpoch: 0,
      };
    }
  }

  /**
   * Fetch metrics from Aztec L2 nodes
   */
  private async getAztecMetrics(aztecUrl: string): Promise<AztecClientMetrics> {
    try {
      const resp = await fetch(aztecUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          method: 'pxe_getNodeInfo',
          params: [],
          id: 67,
        }),
      });

      const data = await resp.json();
      const result = data.result;

      if (!result) {
        throw new Error('No result from Aztec node');
      }

      const latestBlock = result.latest?.number || 0;
      const provenBlock = result.proven?.number || 0;
      const finalizedBlock = result.finalized?.number || 0;

      // Calculate participation as proof progress
      const participation =
        latestBlock > 0 ? (provenBlock / latestBlock) * 100 : 0;

      // Consider syncing if proven is far behind latest
      const syncing = latestBlock - provenBlock > 10;

      return {
        peers: 0, // Aztec doesn't expose peer count
        version: 'Aztec Node',
        syncing,
        blockNumber: latestBlock,
        chainId: 11155111, // Sepolia (where Aztec testnet runs)
        finalizedEpoch: provenBlock,
        participation: Math.round(participation),
        latestBlock,
        provenBlock,
        finalizedBlock,
      };
    } catch (err) {
      console.error('Failed to fetch Aztec metrics:', err);
      return {
        peers: 0,
        version: 'Aztec Node',
        syncing: false,
        blockNumber: 0,
        chainId: 11155111,
        finalizedEpoch: 0,
        participation: 0,
      };
    }
  }

  /**
   * Fetch metrics from eRPC proxy
   * The url should be the network-specific RPC endpoint (e.g. http://erpc.erpc.svc.cluster.local/rpc/mainnet)
   */
  private async getErpcMetrics(erpcUrl: string): Promise<ErpcClientMetrics> {
    try {
      const baseUrl = new URL(erpcUrl).origin;

      const [blockNumberResult, chainIdResult, upstreamCountResult] =
        await Promise.allSettled([
          this.fetchBlockNumber(erpcUrl),
          this.fetchChainId(erpcUrl),
          this.fetchErpcUpstreamCount(baseUrl),
        ]);

      return {
        peers: 0,
        version: 'eRPC',
        syncing: false,
        blockNumber:
          blockNumberResult.status === 'fulfilled'
            ? blockNumberResult.value
            : 0,
        chainId: chainIdResult.status === 'fulfilled' ? chainIdResult.value : 1,
        upstreamCount:
          upstreamCountResult.status === 'fulfilled'
            ? upstreamCountResult.value
            : 0,
      };
    } catch (err) {
      console.error('Failed to fetch eRPC metrics:', err);
      return {
        peers: 0,
        version: 'eRPC',
        syncing: false,
        blockNumber: 0,
        chainId: 1,
        upstreamCount: 0,
      };
    }
  }

  /**
   * Fetch the total number of configured upstreams from the eRPC admin taxonomy endpoint.
   * Returns 0 gracefully if the admin endpoint is unavailable or requires auth.
   */
  private async fetchErpcUpstreamCount(baseUrl: string): Promise<number> {
    try {
      const resp = await fetch(`${baseUrl}/admin`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          method: 'erpc_taxonomy',
          params: [],
          id: 1,
        }),
      });
      const data = await resp.json();
      const projects: Array<{
        networks?: Array<{ upstreams?: unknown[] }>;
      }> = data.result?.projects || [];
      let count = 0;
      for (const project of projects) {
        for (const network of project.networks || []) {
          count += (network.upstreams || []).length;
        }
      }
      return count;
    } catch {
      return 0;
    }
  }

  /** ------------------- Helper methods ------------------- */

  private async fetchWeb3ClientVersion(url: string): Promise<string> {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        jsonrpc: '2.0',
        method: 'web3_clientVersion',
        params: [],
        id: 1,
      }),
    });

    const data = await resp.json();
    return data.result || 'Unknown';
  }

  private async fetchNetPeerCount(url: string): Promise<number> {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        jsonrpc: '2.0',
        method: 'net_peerCount',
        params: [],
        id: 1,
      }),
    });
    const data = await resp.json();
    return data.result ? parseInt(data.result, 16) : 0;
  }

  private async fetchBlockNumber(url: string): Promise<number> {
    console.log(`Fetching block number from ${url}`);
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        jsonrpc: '2.0',
        method: 'eth_blockNumber',
        params: [],
        id: 1,
      }),
    });
    const data = await resp.json();
    return data.result ? parseInt(data.result, 16) : 0;
  }

  private async fetchChainId(url: string): Promise<number> {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        jsonrpc: '2.0',
        method: 'eth_chainId',
        params: [],
        id: 1,
      }),
    });
    const data = await resp.json();
    return data.result ? parseInt(data.result, 16) : 0;
  }

  private async fetchIsSyncing(url: string): Promise<boolean> {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        jsonrpc: '2.0',
        method: 'eth_syncing',
        params: [],
        id: 1,
      }),
    });
    const data = await resp.json();
    return data.result !== false;
  }

  private async fetchFinalizedEpochFromBeacon(
    beaconUrl: string
  ): Promise<number> {
    const resp = await fetch(
      `${beaconUrl}/eth/v1/beacon/states/head/finality_checkpoints`
    );
    const data = await resp.json();
    return data.data?.finalized?.epoch
      ? parseInt(data.data.finalized.epoch)
      : 0;
  }

  private async fetchParticipationCached(beaconUrl: string): Promise<number> {
    const cached = ClientMetricsService.participationCache.get(beaconUrl);
    if (cached && Date.now() < cached.expiresAt) {
      return cached.value;
    }

    try {
      const resp = await fetch(`${beaconUrl}/eth/v1/validator/participation`, {
        signal: AbortSignal.timeout(3000),
      });
      if (!resp.ok) return cached?.value ?? 0;
      const data = await resp.json();
      const value = data.data?.globalParticipationRate
        ? parseFloat(data.data.globalParticipationRate)
        : 0;
      ClientMetricsService.participationCache.set(beaconUrl, {
        value,
        expiresAt: Date.now() + ClientMetricsService.PARTICIPATION_TTL_MS,
      });
      return value;
    } catch {
      return cached?.value ?? 0;
    }
  }

  private async fetchConsensusVersion(url: string): Promise<string> {
    const resp = await fetch(`${url}/eth/v1/node/version`);
    const data = await resp.json();
    return data.data?.version || 'Unknown';
  }

  private async fetchConsensusPeers(url: string): Promise<number> {
    const resp = await fetch(`${url}/eth/v1/node/peer_count`);
    const data = await resp.json();
    const val = data.data?.connected;
    return typeof val === 'string' ? parseInt(val, 10) : val || 0;
  }

  private async fetchConsensusSyncing(url: string): Promise<boolean> {
    const resp = await fetch(`${url}/eth/v1/node/syncing`);
    const data = await resp.json();
    return data.data?.is_syncing || false;
  }

  private async fetchConsensusHeadSlot(url: string): Promise<number> {
    const resp = await fetch(`${url}/eth/v1/beacon/headers/head`);
    const data = await resp.json();
    return data.data?.header?.message?.slot
      ? parseInt(data.data.header.message.slot)
      : 0;
  }
}
