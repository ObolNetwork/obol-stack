/**
 * Service for fetching real-time metrics from Ethereum clients
 */

interface ExecutionClientMetrics {
  peers: number;
  version: string;
  syncing: boolean;
  blockNumber: number;
}

interface ConsensusClientMetrics {
  peers: number;
  version: string;
  syncing: boolean;
  headSlot: number;
  finalizedEpoch: number;
}

export class ClientMetricsService {
  private executionUrl: string;
  private consensusUrl: string;

  constructor(executionUrl?: string, consensusUrl?: string) {
    // Use internal Kubernetes DNS names when running in cluster
    this.executionUrl =
      executionUrl || 'http://l1-full-node-execution.l1.svc.cluster.local:8545';
    this.consensusUrl =
      consensusUrl || 'http://l1-full-node-beacon.l1.svc.cluster.local:5052';
  }

  /**
   * Fetch metrics from Nethermind execution client
   */
  async getExecutionMetrics(): Promise<ExecutionClientMetrics> {
    try {
      // Get peer count from admin_peers or net_peerCount
      const peerResponse = await fetch(this.executionUrl, {
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
      const versionResponse = await fetch(this.executionUrl, {
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
      const syncResponse = await fetch(this.executionUrl, {
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
      const blockResponse = await fetch(this.executionUrl, {
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

      return {
        peers: peerCount,
        version,
        syncing,
        blockNumber,
      };
    } catch (error) {
      console.error('Failed to fetch execution metrics:', error);
      // Return defaults if fetch fails
      return {
        peers: 0,
        version: 'Unknown',
        syncing: false,
        blockNumber: 0,
      };
    }
  }

  /**
   * Fetch metrics from Nimbus consensus client
   */
  async getConsensusMetrics(): Promise<ConsensusClientMetrics> {
    try {
      // Get node version
      const versionResponse = await fetch(
        `${this.consensusUrl}/eth/v1/node/version`
      );
      const versionData = await versionResponse.json();
      const version = versionData.data?.version || 'Unknown';

      // Get peer count
      const peersResponse = await fetch(
        `${this.consensusUrl}/eth/v1/node/peer_count`
      );
      const peersData = await peersResponse.json();
      // Parse connected peers as integer if it's a string
      const peers =
        typeof peersData.data?.connected === 'string'
          ? parseInt(peersData.data.connected, 10)
          : peersData.data?.connected || 0;

      // Get sync status
      const syncResponse = await fetch(
        `${this.consensusUrl}/eth/v1/node/syncing`
      );
      const syncData = await syncResponse.json();
      const syncing = syncData.data?.is_syncing || false;

      // Get head slot
      const headResponse = await fetch(
        `${this.consensusUrl}/eth/v1/beacon/headers/head`
      );
      const headData = await headResponse.json();
      const headSlot = headData.data?.header?.message?.slot
        ? parseInt(headData.data.header.message.slot)
        : 0;

      // Get finalized checkpoint
      const finalityResponse = await fetch(
        `${this.consensusUrl}/eth/v1/beacon/states/head/finality_checkpoints`
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

  /**
   * Fetch all client metrics
   */
  async getAllMetrics() {
    const [execution, consensus] = await Promise.allSettled([
      this.getExecutionMetrics(),
      this.getConsensusMetrics(),
    ]);

    return {
      execution: execution.status === 'fulfilled' ? execution.value : null,
      consensus: consensus.status === 'fulfilled' ? consensus.value : null,
    };
  }
}
