import { DashboardData } from '../../../types/dashboard';

export type NodeStatus =
  | 'healthy'
  | 'behind'
  | 'high-usage'
  | 'stalled'
  | 'low-peers'
  | 'disk-bottleneck'
  | 'low-disk';

/**
 * Thresholds for node health status determination
 */
export const STATUS_THRESHOLDS = {
  /** Critical disk usage threshold (percentage) */
  DISK_CRITICAL: 90,
  /** High resource usage threshold for CPU/Memory (percentage) */
  RESOURCE_HIGH: 80,
  /** Low peer count threshold */
  PEERS_LOW: 10,
} as const;

export function determineNodeStatus(data: DashboardData): NodeStatus {
  // Check for low disk space first (most critical)
  if (
    data.nodeOverview.diskUsage.percentage > STATUS_THRESHOLDS.DISK_CRITICAL
  ) {
    return 'low-disk';
  }

  // Handle case when no chains are available
  if (data.apps.length === 0) {
    return 'stalled'; // No chains means no sync possible
  }

  // Check for stalled sync (not syncing and not on chain tip)
  const isStalled =
    !data.apps.some(app =>
      app.clients.some(client => client.status === 'syncing')
    ) &&
    data.apps.some(app =>
      app.clients.some(client => client.status === 'error')
    );

  if (isStalled) {
    return 'stalled';
  }

  // Check for high resource usage (CPU or Memory > threshold)
  if (
    data.nodeOverview.cpuLoad.percentage > STATUS_THRESHOLDS.RESOURCE_HIGH ||
    data.nodeOverview.memoryUsage.percentage > STATUS_THRESHOLDS.RESOURCE_HIGH
  ) {
    return 'high-usage';
  }

  // Check for low peer count (< threshold)
  const hasLowPeers = data.apps.some(app =>
    app.clients.some(
      client => (client.peers || 0) < STATUS_THRESHOLDS.PEERS_LOW
    )
  );

  if (hasLowPeers) {
    return 'low-peers';
  }

  // Check for slightly behind (syncing but behind by < 1000 blocks)
  // This would need to be calculated based on current block height vs expected
  // For now, we'll assume if syncing is true, it's either healthy or behind
  const isSyncing = data.apps.some(app =>
    app.clients.some(client => client.status === 'syncing')
  );

  if (isSyncing) {
    // In a real implementation, you'd compare current block height with expected
    // For now, we'll assume if syncing, it might be behind
    return 'behind';
  }

  // Default to healthy if none of the above conditions are met
  return 'healthy';
}
