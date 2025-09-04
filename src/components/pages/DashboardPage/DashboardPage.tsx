import { DashboardTemplate } from '../../templates/DashboardTemplate';
import { DashboardHeader } from '../../organisms/DashboardHeader';
import { DashboardContent } from '../../organisms/DashboardContent';

// Mock data aligned with PRD specifications
const mockNodeData = {
  environment: 'mainnet' as const,
  mode: 'Full Mode',
  executionClient: {
    name: 'Geth',
    version: '1.13.5',
    status: 'synced',
    icon: '🟦',
  },
  consensusClient: {
    name: 'Prysm',
    version: '4.1.1',
    status: 'synced',
    icon: '🟪',
  },
  syncStatus: {
    isSync: true,
    progress: 100,
    distanceToJustified: 0,
  },
  chainData: {
    network: 'mainnet',
    blockHeight: 18972341,
    slotHeight: 8123456,
    headSlot: 8123456,
    finalizedEpoch: 254170,
    finalizedSlot: 8133440,
  },
  peerCount: 89,
  systemMetrics: {
    diskUsage: { used: 1247.8, total: 2000, unit: 'GB' },
    memoryUsage: { used: 6.2, total: 32, unit: 'GB' },
    cpuLoad: 18.5,
    uptime: { days: 7, hours: 3, minutes: 22 },
  },
  portStatus: {
    p2p: { port: 30303, status: 'open' as const },
    jsonRpc: { port: 8545, status: 'open' as const },
    prometheus: { port: 9090, status: 'open' as const },
    beacon: { port: 4000, status: 'open' as const },
  },
  chainHealth: {
    participationRate: 97.8,
    attestationRate: 99.2,
  },
};

export function DashboardPage() {
  return (
    <DashboardTemplate
      header={<DashboardHeader />}
      content={<DashboardContent />}
    />
  );
}
