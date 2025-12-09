/**
 * Service for fetching real-time metrics from Ethereum and Aztec clients
 */

export type ClientType = 'execution' | 'consensus' | 'helios' | 'aztec';

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
  constructor() {}

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
      const participation =
        await this.fetchParticipationFromBeacon(consensusUrl);

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

  private async fetchParticipationFromBeacon(
    beaconUrl: string,
    fallback = 0
  ): Promise<number> {
    try {
      const resp = await fetch(`${beaconUrl}/eth/v1/validator/participation`);
      const data = await resp.json();
      return data.data?.globalParticipationRate
        ? parseFloat(data.data.globalParticipationRate)
        : fallback;
    } catch {
      return fallback;
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
