import { DashboardData } from '../../../types/dashboard';

export type NodeStatus =
  | 'healthy'
  | 'behind'
  | 'high-usage'
  | 'stalled'
  | 'low-peers'
  | 'disk-bottleneck'
  | 'low-disk';

export function determineNodeStatus(data: DashboardData): NodeStatus {
  // Check for low disk space first (most critical)
  if (data.nodeOverview.diskUsage.percentage > 90) {
    return 'low-disk';
  }

  // Handle case when no chains are available
  if (data.chains.length === 0) {
    return 'stalled'; // No chains means no sync possible
  }

  // Check for stalled sync (not syncing and not on chain tip)
  const isStalled =
    !data.chains.some(chain =>
      chain.clients.some(client => client.status === 'syncing')
    ) &&
    data.chains.some(chain =>
      chain.clients.some(client => client.status === 'error')
    );

  if (isStalled) {
    return 'stalled';
  }

  // Check for high resource usage (CPU or Memory > 80%)
  if (
    data.nodeOverview.cpuLoad.percentage > 80 ||
    data.nodeOverview.memoryUsage.percentage > 80
  ) {
    return 'high-usage';
  }

  // Check for low peer count (< 10 peers)
  const hasLowPeers = data.chains.some(chain =>
    chain.clients.some(client => (client.peers || 0) < 10)
  );

  if (hasLowPeers) {
    return 'low-peers';
  }

  // Check for slightly behind (syncing but behind by < 1000 blocks)
  // This would need to be calculated based on current block height vs expected
  // For now, we'll assume if syncing is true, it's either healthy or behind
  const isSyncing = data.chains.some(chain =>
    chain.clients.some(client => client.status === 'syncing')
  );

  if (isSyncing) {
    // In a real implementation, you'd compare current block height with expected
    // For now, we'll assume if syncing, it might be behind
    return 'behind';
  }

  // Default to healthy if none of the above conditions are met
  return 'healthy';
}
