/**
 * eRPC client for fetching Ethereum RPC metrics and status
 */

interface ERPCStatus {
  status: string;
  message: string;
  details: {
    rpc: {
      status: string;
      message: string;
      upstreams: Record<string, {
        network: string;
        metrics: {
          requestsTotal: number;
          errorsTotal: number;
          blockHeadLag: number;
          finalizationLag: number;
          cordoned: boolean;
          cordonedReason: string | null;
          errorRate: number;
          throttledRate: number;
          responseQuantiles: {
            p50: number;
            p70: number;
            p90: number;
            p95: number;
            p99: number;
          };
        };
      }>;
    };
  };
}

interface JsonRpcResponse {
  jsonrpc: string;
  id: number;
  result?: unknown;
  error?: {
    code: number;
    message: string;
  };
}

export class ERPCClient {
  private baseUrl: string;

  constructor(baseUrl: string = process.env.ERPC_URL || 'http://obol.stack') {
    this.baseUrl = baseUrl;
  }

  /**
   * Get eRPC health status
   */
  async getStatus(): Promise<ERPCStatus> {
    // When accessing internally, the health endpoint is at the root
    const healthUrl = this.baseUrl.includes('cluster.local') 
      ? `${this.baseUrl}/` 
      : `${this.baseUrl}/rpc`;
    
    const response = await fetch(healthUrl, {
      headers: {
        'Accept': 'application/json',
      },
    });

    if (!response.ok) {
      throw new Error(`eRPC status request failed: ${response.statusText}`);
    }

    return response.json();
  }

  /**
   * Execute an Ethereum JSON-RPC call
   */
  async rpcCall(network: string, method: string, params: unknown[] = []): Promise<JsonRpcResponse> {
    // When accessing internally, eRPC is at port 80 root path
    // When accessing externally via ingress, it's at /rpc/{network}
    const rpcUrl = this.baseUrl.includes('cluster.local')
      ? this.baseUrl  // Internal: direct to service
      : `${this.baseUrl}/rpc/${network}`;  // External: via ingress
    
    const response = await fetch(rpcUrl, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Network-Id': `evm:${network === 'mainnet' ? '1' : '560048'}`,  // Add network header for eRPC
      },
      body: JSON.stringify({
        jsonrpc: '2.0',
        method,
        params,
        id: 1,
      }),
    });

    if (!response.ok) {
      throw new Error(`RPC call failed: ${response.statusText}`);
    }

    return response.json();
  }

  /**
   * Get current block number for a network
   */
  async getBlockNumber(network: string = 'hoodi'): Promise<number> {
    try {
      const response = await this.rpcCall(network, 'eth_blockNumber');
      
      if (response.error) {
        throw new Error(response.error.message);
      }
      
      // Convert hex to number
      return parseInt(response.result, 16);
    } catch (error) {
      console.error(`Failed to fetch block number for ${network}:`, error);
      throw error;
    }
  }

  /**
   * Get sync status for a network
   */
  async getSyncStatus(network: string = 'hoodi'): Promise<{ synced: boolean; currentBlock?: number; highestBlock?: number; startingBlock?: number }> {
    try {
      const response = await this.rpcCall(network, 'eth_syncing');
      
      if (response.error) {
        throw new Error(response.error.message);
      }
      
      // If result is false, the node is synced
      if (response.result === false) {
        return { synced: true };
      }
      
      // Otherwise, return the sync progress
      return {
        synced: false,
        currentBlock: parseInt(response.result.currentBlock, 16),
        highestBlock: parseInt(response.result.highestBlock, 16),
        startingBlock: parseInt(response.result.startingBlock, 16),
      };
    } catch (error) {
      console.error(`Failed to fetch sync status for ${network}:`, error);
      throw error;
    }
  }

  /**
   * Get peer count for a network
   */
  async getPeerCount(network: string = 'hoodi'): Promise<number> {
    try {
      const response = await this.rpcCall(network, 'net_peerCount');
      
      if (response.error) {
        throw new Error(response.error.message);
      }
      
      // Convert hex to number
      return parseInt(response.result, 16);
    } catch (error) {
      console.error(`Failed to fetch peer count for ${network}:`, error);
      throw error;
    }
  }

  /**
   * Get client version
   */
  async getClientVersion(network: string = 'hoodi'): Promise<string> {
    try {
      const response = await this.rpcCall(network, 'web3_clientVersion');
      
      if (response.error) {
        throw new Error(response.error.message);
      }
      
      return response.result;
    } catch (error) {
      console.error(`Failed to fetch client version for ${network}:`, error);
      throw error;
    }
  }

  /**
   * Get aggregated network metrics
   */
  async getNetworkMetrics(network: string = 'hoodi') {
    try {
      const [blockNumber, syncStatus, status] = await Promise.all([
        this.getBlockNumber(network).catch(() => 0),
        this.getSyncStatus(network).catch(() => ({ synced: false })),
        this.getStatus().catch(() => null),
      ]);

      // Find the upstream for this network
      const upstream = status?.details?.rpc?.upstreams 
        ? Object.values(status.details.rpc.upstreams).find(u => 
            u.network === `evm:${network === 'mainnet' ? '1' : '560048'}`
          )
        : null;

      return {
        blockHeight: blockNumber,
        synced: syncStatus.synced,
        syncProgress: syncStatus.synced ? 100 : 
          (syncStatus.currentBlock && syncStatus.highestBlock 
            ? (syncStatus.currentBlock / syncStatus.highestBlock) * 100 
            : 0),
        metrics: upstream?.metrics || null,
      };
    } catch (error) {
      console.error(`Failed to fetch network metrics for ${network}:`, error);
      throw error;
    }
  }
}