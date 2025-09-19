/**
 * Service for fetching real-time metrics from Ethereum clients
 */

export interface ExecutionClientMetrics {
  peers: number;
  version: string;
  syncing: boolean;
  blockNumber: number;
  chainId: number;
}

export interface ConsensusClientMetrics {
  peers: number;
  version: string;
  syncing: boolean;
  headSlot: number;
  finalizedEpoch: number;
}

export class ClientMetricsService {
  constructor() {
    // Use internal Kubernetes DNS names when running in cluster
    // this.executionUrl =
    //   executionUrl || 'http://l1-full-node-execution.l1.svc.cluster.local:8545';
    // this.consensusUrl =
    //   consensusUrl || 'http://l1-full-node-beacon.l1.svc.cluster.local:5052';
  }

  /**
   * Unified method to fetch client metrics
   * @param type - 'execution' or 'consensus'
   * @param url - The client endpoint URL
   * @returns Client metrics based on type
   */
  async getMetrics(
    type: 'execution' | 'consensus',
    url: string
  ): Promise<ExecutionClientMetrics | ConsensusClientMetrics> {
    if (type === 'execution') {
      return this.getExecutionMetrics(url);
    } else {
      return this.getConsensusMetrics(url);
    }
  }

  /**
   * Fetch metrics from Nethermind execution client
   */
  async getExecutionMetrics(
    executionUrl: string
  ): Promise<ExecutionClientMetrics> {
    try {
      // Get peer count from admin_peers or net_peerCount
      const peerResponse = await fetch(executionUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          method: 'net_peerCount',
          params: [],
          id: 1,
        }),
      });

      const peerData = await peerResponse.json();
      const peerCount = peerData.result ? parseInt(peerData.result, 16) : 0;

      // Get version
      const versionResponse = await fetch(executionUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          method: 'web3_clientVersion',
          params: [],
          id: 1,
        }),
      });

      const versionData = await versionResponse.json();
      const version = versionData.result || 'Unknown';

      // Get sync status
      const syncResponse = await fetch(executionUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          method: 'eth_syncing',
          params: [],
          id: 1,
        }),
      });

      const syncData = await syncResponse.json();
      const syncing = syncData.result !== false;

      // Get current block number
      const blockResponse = await fetch(executionUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          method: 'eth_blockNumber',
          params: [],
          id: 1,
        }),
      });

      const blockData = await blockResponse.json();
      const blockNumber = blockData.result ? parseInt(blockData.result, 16) : 0;

      // Get chain ID
      const chainIdResponse = await fetch(executionUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          jsonrpc: '2.0',
          method: 'eth_chainId',
          params: [],
          id: 1,
        }),
      });

      const chainIdData = await chainIdResponse.json();
      const chainId = chainIdData.result ? parseInt(chainIdData.result, 16) : 0;

      return {
        peers: peerCount,
        version,
        syncing,
        blockNumber,
        chainId,
      };
    } catch (error) {
      console.error('Failed to fetch execution metrics:', error);
      // Return defaults if fetch fails
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
   * Fetch metrics from Nimbus consensus client
   */
  async getConsensusMetrics(
    consensusUrl: string
  ): Promise<ConsensusClientMetrics> {
    try {
      // Get node version
      const versionResponse = await fetch(
        `${consensusUrl}/eth/v1/node/version`
      );
      const versionData = await versionResponse.json();
      const version = versionData.data?.version || 'Unknown';

      // Get peer count
      const peersResponse = await fetch(
        `${consensusUrl}/eth/v1/node/peer_count`
      );
      const peersData = await peersResponse.json();
      // Parse connected peers as integer if it's a string
      const peers =
        typeof peersData.data?.connected === 'string'
          ? parseInt(peersData.data.connected, 10)
          : peersData.data?.connected || 0;

      // Get sync status
      const syncResponse = await fetch(`${consensusUrl}/eth/v1/node/syncing`);
      const syncData = await syncResponse.json();
      const syncing = syncData.data?.is_syncing || false;

      // Get head slot
      const headResponse = await fetch(
        `${consensusUrl}/eth/v1/beacon/headers/head`
      );
      const headData = await headResponse.json();
      const headSlot = headData.data?.header?.message?.slot
        ? parseInt(headData.data.header.message.slot)
        : 0;

      // Get finalized checkpoint
      const finalityResponse = await fetch(
        `${consensusUrl}/eth/v1/beacon/states/head/finality_checkpoints`
      );
      const finalityData = await finalityResponse.json();
      const finalizedEpoch = finalityData.data?.finalized?.epoch
        ? parseInt(finalityData.data.finalized.epoch)
        : 0;

      return {
        peers,
        version,
        syncing,
        headSlot,
        finalizedEpoch,
      };
    } catch (error) {
      console.error('Failed to fetch consensus metrics:', error);
      // Return defaults if fetch fails
      return {
        peers: 0,
        version: 'Unknown',
        syncing: false,
        headSlot: 0,
        finalizedEpoch: 0,
      };
    }
  }
}
