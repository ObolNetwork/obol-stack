'use client';

import Image from 'next/image';
import { 
  Cpu, 
  HardDrive, 
  Activity, 
  Globe, 
  Users, 
  CheckCircle, 
  AlertTriangle, 
  XCircle,
  Clock,
  Wifi,
  ExternalLink,
  Download,
  FileText
} from 'lucide-react';

// Mock data aligned with PRD specifications
const mockNodeData = {
  environment: 'mainnet', // mainnet, hoodi, anvil
  mode: 'Full Mode', // Full Mode, Light Mode
  executionClient: { 
    name: 'Geth', 
    version: '1.13.5', 
    status: 'synced',
    icon: '🟦' // Geth icon representation
  },
  consensusClient: { 
    name: 'Prysm', 
    version: '4.1.1', 
    status: 'synced',
    icon: '🟪' // Prysm icon representation
  },
  syncStatus: { 
    isSync: true, 
    progress: 100,
    distanceToJustified: 0 // Distance to latest justified epoch
  },
  chainData: {
    network: 'mainnet',
    blockHeight: 18972341,
    slotHeight: 8123456,
    headSlot: 8123456,
    finalizedEpoch: 254170,
    finalizedSlot: 8133440
  },
  peerCount: 89, // Updated peer count
  systemMetrics: {
    diskUsage: { used: 1247.8, total: 2000, unit: 'GB' }, // More realistic for mainnet
    memoryUsage: { used: 6.2, total: 32, unit: 'GB' }, // Higher memory usage
    cpuLoad: 18.5,
    uptime: { days: 7, hours: 3, minutes: 22 }
  },
  portStatus: {
    p2p: { port: 30303, status: 'open' },
    jsonRpc: { port: 8545, status: 'open' },
    prometheus: { port: 9090, status: 'open' },
    beacon: { port: 4000, status: 'open' }
  },
  chainHealth: {
    participationRate: 97.8, // Network participation rate
    attestationRate: 99.2 // Recent attestation rate
  }
};

const StatusIcon = ({ status }: { status: 'synced' | 'syncing' | 'error' }) => {
  switch (status) {
    case 'synced':
      return <CheckCircle className="w-5 h-5 text-obol-green" />;
    case 'syncing':
      return <Activity className="w-5 h-5 text-obol-orange animate-pulse" />;
    case 'error':
      return <XCircle className="w-5 h-5 text-red-500" />;
    default:
      return <AlertTriangle className="w-5 h-5 text-obol-text-muted" />;
  }
};

export default function ObolStackDashboard() {
  return (
    <div className="min-h-screen bg-colors-bg01 text-white">
      {/* Launchpad-style background with subtle pattern */}
      <div className="fixed inset-0 pointer-events-none">
        <div className="absolute top-0 left-0 w-1/2 h-1/2 bg-gradient-radial from-obol-green/5 via-transparent to-transparent"></div>
      </div>
      
      <div className="relative max-w-7xl mx-auto py-6 px-4">
        {/* Launchpad-style Header */}
        <div className="relative mb-8">
          {/* Main header container with Launchpad styling */}
          <div className="bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 shadow-2xl">
            <div className="p-6">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-6">
                  <div className="relative">
                    <Image 
                      src="/obol-logo.png" 
                      alt="Obol Network" 
                      width={48} 
                      height={48}
                      className="rounded-full"
                    />
                    <div className="absolute -bottom-1 -right-1 w-3 h-3 bg-obol-green rounded-full border-2 border-colors-cardBackgroundDark"></div>
                  </div>
                  <div>
                    <h1 className="text-2xl font-bold text-white font-sans mb-1">Obol Stack Dashboard</h1>
                    <p className="text-obol-text-muted text-sm font-medium">Node monitoring and cluster management</p>
                  </div>
                </div>
                
                <div className="flex items-center gap-3">
                  <div className="px-3 py-1.5 bg-obol-green/20 border border-obol-green/30 rounded-lg">
                    <span className="text-obol-green text-sm font-semibold uppercase tracking-wide">
                      {mockNodeData.environment}
                    </span>
                  </div>
                  <div className="px-3 py-1.5 bg-obol-blue/20 border border-obol-blue/30 rounded-lg">
                    <span className="text-obol-light-blue text-sm font-semibold">
                      {mockNodeData.mode}
                    </span>
                  </div>
                  <div className="flex items-center gap-2 px-3 py-1.5 bg-colors-bg03 rounded-lg">
                    <StatusIcon status={mockNodeData.executionClient.status as any} />
                    <span className="text-obol-green text-sm font-medium">Online</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        {/* Node Overview Section */}
        <div className="mb-8">
          <div className="flex items-center gap-3 mb-6">
            <div className="w-1 h-6 bg-gradient-to-b from-obol-green to-obol-light-green rounded-full"></div>
            <h2 className="text-xl font-semibold text-white font-sans">Node Overview</h2>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
            {/* Execution Client Card */}
            <div className="group relative bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 hover:border-obol-green/30 transition-all duration-300 shadow-lg hover:shadow-xl">
              <div className="p-6">
                <div className="flex items-center gap-3 mb-4">
                  <div className="w-10 h-10 bg-obol-blue rounded-xl flex items-center justify-center text-white font-bold text-lg">
                    G
                  </div>
                  <h3 className="font-semibold text-white font-sans">Execution Client</h3>
                </div>
                <div className="text-xl font-bold text-white font-sans mb-1">
                  {mockNodeData.executionClient.name}
                </div>
                <div className="text-obol-text-muted text-sm font-medium mb-4">
                  v{mockNodeData.executionClient.version}
                </div>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <StatusIcon status={mockNodeData.executionClient.status as any} />
                    <span className="text-sm text-obol-green capitalize font-medium">
                      {mockNodeData.executionClient.status}
                    </span>
                  </div>
                  <div className="w-2 h-2 bg-obol-green rounded-full animate-pulse"></div>
                </div>
              </div>
            </div>

            {/* Consensus Client Card */}
            <div className="group relative bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 hover:border-obol-purple/30 transition-all duration-300 shadow-lg hover:shadow-xl">
              <div className="p-6">
                <div className="flex items-center gap-3 mb-4">
                  <div className="w-10 h-10 bg-obol-purple rounded-xl flex items-center justify-center text-white font-bold text-lg">
                    P
                  </div>
                  <h3 className="font-semibold text-white font-sans">Consensus Client</h3>
                </div>
                <div className="text-xl font-bold text-white font-sans mb-1">
                  {mockNodeData.consensusClient.name}
                </div>
                <div className="text-obol-text-muted text-sm font-medium mb-4">
                  v{mockNodeData.consensusClient.version}
                </div>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <StatusIcon status={mockNodeData.consensusClient.status as any} />
                    <span className="text-sm text-obol-green capitalize font-medium">
                      {mockNodeData.consensusClient.status}
                    </span>
                  </div>
                  <div className="w-2 h-2 bg-obol-purple rounded-full animate-pulse"></div>
                </div>
              </div>
            </div>

            {/* Sync Status Card */}
            <div className="group relative bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 hover:border-obol-green/30 transition-all duration-300 shadow-lg hover:shadow-xl">
              <div className="p-6">
                <div className="flex items-center gap-3 mb-4">
                  <div className="w-10 h-10 bg-obol-green rounded-xl flex items-center justify-center">
                    <Wifi className="w-5 h-5 text-white" />
                  </div>
                  <h3 className="font-semibold text-white font-sans">Sync Status</h3>
                </div>
                <div className="flex items-center gap-2 mb-4">
                  <div className="text-xl font-bold text-obol-green font-sans">
                    Synced
                  </div>
                  <div className="w-2 h-2 bg-obol-green rounded-full animate-pulse"></div>
                </div>
                <div className="space-y-2 mb-4">
                  <div className="text-obol-text-muted text-sm font-mono">
                    Block: {mockNodeData.chainData.blockHeight.toLocaleString()}
                  </div>
                  <div className="text-obol-text-muted text-sm font-mono">
                    Head Slot: {mockNodeData.chainData.headSlot.toLocaleString()}
                  </div>
                  <div className="text-obol-text-muted text-sm font-mono">
                    Distance to justified: {mockNodeData.syncStatus.distanceToJustified}
                  </div>
                </div>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Users className="w-4 h-4 text-obol-light-blue" />
                    <span className="text-sm text-obol-light-blue font-medium">
                      {mockNodeData.peerCount} peers
                    </span>
                  </div>
                  <div className="px-2 py-1 bg-obol-green/20 border border-obol-green/30 text-obol-green text-xs font-semibold rounded-lg">
                    LIVE
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        {/* System Metrics Section */}
        <div className="mb-8">
          <div className="flex items-center gap-3 mb-6">
            <div className="w-1 h-6 bg-gradient-to-b from-obol-orange to-obol-light-blue rounded-full"></div>
            <h2 className="text-xl font-semibold text-white font-sans">System Metrics</h2>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6 mb-6">
            {/* Disk Usage */}
            <div className="bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 hover:border-obol-orange/30 transition-all duration-300 shadow-lg hover:shadow-xl">
              <div className="p-6">
                <div className="flex items-center justify-between mb-3">
                  <div className="flex items-center gap-3">
                    <div className="w-8 h-8 bg-obol-orange rounded-xl flex items-center justify-center">
                      <HardDrive className="w-4 h-4 text-white" />
                    </div>
                    <h3 className="font-semibold text-white font-sans">Disk Usage</h3>
                  </div>
                  <div className="text-xs text-obol-orange font-semibold bg-obol-orange/20 border border-obol-orange/30 px-2 py-1 rounded-lg">
                    {((mockNodeData.systemMetrics.diskUsage.used / mockNodeData.systemMetrics.diskUsage.total) * 100).toFixed(1)}%
                  </div>
                </div>
                <div className="text-2xl font-bold text-white font-sans mb-1">
                  {mockNodeData.systemMetrics.diskUsage.used} GB
                </div>
                <div className="text-obol-text-muted text-sm font-medium mb-4">
                  of {mockNodeData.systemMetrics.diskUsage.total} GB available
                </div>
                <div className="w-full bg-colors-bg03 rounded-full h-2">
                  <div 
                    className="bg-obol-orange h-2 rounded-full transition-all duration-500" 
                    style={{ width: `${(mockNodeData.systemMetrics.diskUsage.used / mockNodeData.systemMetrics.diskUsage.total) * 100}%` }}
                  ></div>
                </div>
              </div>
            </div>

            {/* Memory Usage */}
            <div className="bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 hover:border-obol-light-blue/30 transition-all duration-300 shadow-lg hover:shadow-xl">
              <div className="p-6">
                <div className="flex items-center justify-between mb-3">
                  <div className="flex items-center gap-3">
                    <div className="w-8 h-8 bg-obol-light-blue rounded-xl flex items-center justify-center">
                      <Cpu className="w-4 h-4 text-white" />
                    </div>
                    <h3 className="font-semibold text-white font-sans">Memory Usage</h3>
                  </div>
                  <div className="text-xs text-obol-light-blue font-semibold bg-obol-light-blue/20 border border-obol-light-blue/30 px-2 py-1 rounded-lg">
                    {((mockNodeData.systemMetrics.memoryUsage.used / mockNodeData.systemMetrics.memoryUsage.total) * 100).toFixed(1)}%
                  </div>
                </div>
                <div className="text-2xl font-bold text-white font-sans mb-1">
                  {mockNodeData.systemMetrics.memoryUsage.used} GB
                </div>
                <div className="text-obol-text-muted text-sm font-medium mb-4">
                  of {mockNodeData.systemMetrics.memoryUsage.total} GB available
                </div>
                <div className="w-full bg-colors-bg03 rounded-full h-2">
                  <div 
                    className="bg-obol-light-blue h-2 rounded-full transition-all duration-500" 
                    style={{ width: `${(mockNodeData.systemMetrics.memoryUsage.used / mockNodeData.systemMetrics.memoryUsage.total) * 100}%` }}
                  ></div>
                </div>
              </div>
            </div>

            {/* CPU Load */}
            <div className="bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 hover:border-obol-green/30 transition-all duration-300 shadow-lg hover:shadow-xl">
              <div className="p-6">
                <div className="flex items-center justify-between mb-3">
                  <div className="flex items-center gap-3">
                    <div className="w-8 h-8 bg-obol-green rounded-xl flex items-center justify-center">
                      <Activity className="w-4 h-4 text-white" />
                    </div>
                    <h3 className="font-semibold text-white font-sans">CPU Load</h3>
                  </div>
                  <div className="text-xs text-obol-green font-semibold bg-obol-green/20 border border-obol-green/30 px-2 py-1 rounded-lg">
                    {mockNodeData.systemMetrics.cpuLoad}%
                  </div>
                </div>
                <div className="text-2xl font-bold text-white font-sans mb-1">
                  {mockNodeData.systemMetrics.cpuLoad}%
                </div>
                <div className="text-obol-text-muted text-sm font-medium mb-4">
                  Current utilization
                </div>
                <div className="w-full bg-colors-bg03 rounded-full h-2">
                  <div 
                    className="bg-obol-green h-2 rounded-full transition-all duration-500" 
                    style={{ width: `${mockNodeData.systemMetrics.cpuLoad}%` }}
                  ></div>
                </div>
              </div>
            </div>

            {/* Uptime */}
            <div className="bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 hover:border-obol-purple/30 transition-all duration-300 shadow-lg hover:shadow-xl">
              <div className="p-6">
                <div className="flex items-center justify-between mb-3">
                  <div className="flex items-center gap-3">
                    <div className="w-8 h-8 bg-obol-purple rounded-xl flex items-center justify-center">
                      <Clock className="w-4 h-4 text-white" />
                    </div>
                    <h3 className="font-semibold text-white font-sans">Uptime</h3>
                  </div>
                  <div className="text-xs text-obol-purple font-semibold bg-obol-purple/20 border border-obol-purple/30 px-2 py-1 rounded-lg">
                    STABLE
                  </div>
                </div>
                <div className="text-2xl font-bold text-white font-sans mb-1">
                  {mockNodeData.systemMetrics.uptime.days}d {mockNodeData.systemMetrics.uptime.hours}h
                </div>
                <div className="text-obol-text-muted text-sm font-medium">
                  {mockNodeData.systemMetrics.uptime.minutes}m continuous operation
                </div>
              </div>
            </div>
          </div>

          {/* Port Status */}
          <div className="bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 shadow-lg">
            <div className="p-6">
              <div className="flex items-center justify-between mb-6">
                <h3 className="text-lg font-semibold text-white font-sans">Port Status</h3>
                <div className="flex items-center gap-2">
                  <div className="w-2 h-2 bg-obol-green rounded-full animate-pulse"></div>
                  <span className="text-sm text-obol-green font-semibold">All Open</span>
                </div>
              </div>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <div className="bg-colors-bg03 p-4 rounded-xl text-center hover:bg-colors-bg02 transition-colors">
                  <div className="text-obol-text-muted text-sm font-medium mb-2">P2P</div>
                  <div className="text-white font-mono font-bold text-lg mb-2">{mockNodeData.portStatus.p2p.port}</div>
                  <span className="bg-obol-green/20 border border-obol-green/30 text-obol-green text-xs px-3 py-1 rounded-lg font-semibold">Open</span>
                </div>
                <div className="bg-colors-bg03 p-4 rounded-xl text-center hover:bg-colors-bg02 transition-colors">
                  <div className="text-obol-text-muted text-sm font-medium mb-2">JSON-RPC</div>
                  <div className="text-white font-mono font-bold text-lg mb-2">{mockNodeData.portStatus.jsonRpc.port}</div>
                  <span className="bg-obol-green/20 border border-obol-green/30 text-obol-green text-xs px-3 py-1 rounded-lg font-semibold">Open</span>
                </div>
                <div className="bg-colors-bg03 p-4 rounded-xl text-center hover:bg-colors-bg02 transition-colors">
                  <div className="text-obol-text-muted text-sm font-medium mb-2">Prometheus</div>
                  <div className="text-white font-mono font-bold text-lg mb-2">{mockNodeData.portStatus.prometheus.port}</div>
                  <span className="bg-obol-green/20 border border-obol-green/30 text-obol-green text-xs px-3 py-1 rounded-lg font-semibold">Open</span>
                </div>
                <div className="bg-colors-bg03 p-4 rounded-xl text-center hover:bg-colors-bg02 transition-colors">
                  <div className="text-obol-text-muted text-sm font-medium mb-2">Beacon API</div>
                  <div className="text-white font-mono font-bold text-lg mb-2">{mockNodeData.portStatus.beacon.port}</div>
                  <span className="bg-obol-green/20 border border-obol-green/30 text-obol-green text-xs px-3 py-1 rounded-lg font-semibold">Open</span>
                </div>
              </div>
            </div>
          </div>
        </div>

        {/* Charon Status Section (Coming Soon) */}
        <div className="mb-8 opacity-60">
          <div className="flex items-center gap-3 mb-6">
            <h2 className="text-xl font-semibold text-white font-sans">Charon Status</h2>
            <span className="bg-obol-bg-04 text-obol-text-muted font-medium px-3 py-1 rounded-full text-sm">Coming Soon</span>
          </div>
          <div className="p-6 bg-obol-bg-02 border border-obol-bg-04 border-dashed rounded-lg">
            <p className="text-obol-text-muted mb-6 font-medium">
              Distributed validator middleware status and cluster information will appear here.
            </p>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-6">
              <div className="text-center p-4 bg-obol-bg-03 rounded-lg">
                <div className="text-obol-text-soft text-sm font-medium mb-1">Cluster Name</div>
                <div className="text-obol-text-muted font-mono">obol-cluster-001</div>
              </div>
              <div className="text-center p-4 bg-obol-bg-03 rounded-lg">
                <div className="text-obol-text-soft text-sm font-medium mb-1">Peers Connected</div>
                <div className="text-obol-text-muted font-mono">4 of 4</div>
              </div>
              <div className="text-center p-4 bg-obol-bg-03 rounded-lg">
                <div className="text-obol-text-soft text-sm font-medium mb-1">Signed Duties</div>
                <div className="text-obol-text-muted font-mono">1,247 / 1,250</div>
              </div>
            </div>
            <div className="flex justify-between items-center">
              <div className="text-obol-text-soft text-sm">
                Version: charon v0.19.2
              </div>
              <button 
                className="text-obol-text-soft hover:text-obol-text-muted gap-2 flex items-center opacity-50 cursor-not-allowed"
                disabled
              >
                <ExternalLink className="w-4 h-4" />
                View Launchpad
              </button>
            </div>
          </div>
        </div>

        {/* Chain Status */}
        <div className="mb-8">
          <div className="flex items-center gap-3 mb-6">
            <div className="w-1 h-6 bg-gradient-to-b from-obol-green to-obol-purple rounded-full"></div>
            <h2 className="text-xl font-semibold text-white font-sans">Chain Status</h2>
          </div>
          <div className="bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 hover:border-obol-green/30 transition-all duration-300 shadow-lg">
            <div className="p-6">
              <div className="flex items-center justify-between mb-6">
                <div className="flex items-center gap-4">
                  <div className="w-12 h-12 bg-obol-green rounded-2xl flex items-center justify-center">
                    <Globe className="w-6 h-6 text-white" />
                  </div>
                  <div>
                    <div className="text-xl font-semibold text-white capitalize font-sans">
                      {mockNodeData.chainData.network}
                    </div>
                    <div className="text-obol-text-muted text-sm font-medium">
                      Ethereum Network
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <div className="w-2 h-2 bg-obol-green rounded-full animate-pulse"></div>
                  <span className="bg-obol-green/20 border border-obol-green/30 text-obol-green font-semibold px-4 py-2 rounded-xl text-sm">
                    Healthy
                  </span>
                </div>
              </div>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <div className="bg-colors-bg03 p-4 rounded-xl text-center hover:bg-colors-bg02 transition-colors">
                  <div className="text-obol-text-muted text-sm font-medium mb-2">Finalized Epoch</div>
                  <div className="text-white font-mono text-lg font-bold mb-1">
                    {mockNodeData.chainData.finalizedEpoch.toLocaleString()}
                  </div>
                  <div className="text-obol-text-soft text-xs font-mono">
                    Slot: {mockNodeData.chainData.finalizedSlot.toLocaleString()}
                  </div>
                </div>
                <div className="bg-colors-bg03 p-4 rounded-xl text-center hover:bg-colors-bg02 transition-colors">
                  <div className="text-obol-text-muted text-sm font-medium mb-2">Block Height</div>
                  <div className="text-white font-mono text-lg font-bold mb-1">
                    {mockNodeData.chainData.blockHeight.toLocaleString()}
                  </div>
                  <div className="text-obol-text-soft text-xs">
                    Latest execution block
                  </div>
                </div>
                <div className="bg-colors-bg03 p-4 rounded-xl text-center hover:bg-colors-bg02 transition-colors">
                  <div className="text-obol-text-muted text-sm font-medium mb-2">Node Mode</div>
                  <div className="text-white font-sans text-lg font-bold mb-1">
                    Full Node
                  </div>
                  <div className="text-obol-text-soft text-xs">
                    Complete chain data
                  </div>
                </div>
                <div className="bg-colors-bg03 p-4 rounded-xl text-center hover:bg-colors-bg02 transition-colors">
                  <div className="text-obol-text-muted text-sm font-medium mb-2">Network Participation</div>
                  <div className="text-obol-green font-sans text-lg font-bold mb-1">
                    {mockNodeData.chainHealth.participationRate}%
                  </div>
                  <div className="text-obol-text-soft text-xs">
                    Recent attestation rate
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div className="border-t border-obol-bg-04 my-8"></div>

        {/* Footer */}
        <div className="bg-colors-cardBackgroundDark rounded-2xl border border-colors-bg03 shadow-lg">
          <div className="p-6">
            <div className="flex justify-between items-center">
              <div className="flex gap-3">
                <button 
                  className="group bg-colors-bg03 hover:bg-obol-green/20 text-obol-text-muted hover:text-obol-green font-medium gap-2 flex items-center transition-all duration-300 px-4 py-2 rounded-xl border border-colors-bg02 hover:border-obol-green/30"
                  onClick={() => {
                    const diagnostics = {
                      timestamp: new Date().toISOString(),
                      environment: mockNodeData.environment,
                      mode: mockNodeData.mode,
                      clients: {
                        execution: mockNodeData.executionClient,
                        consensus: mockNodeData.consensusClient
                      },
                      sync: mockNodeData.syncStatus,
                      chain: mockNodeData.chainData,
                      peers: mockNodeData.peerCount,
                      system: mockNodeData.systemMetrics,
                      ports: mockNodeData.portStatus,
                      health: mockNodeData.chainHealth
                    };
                    navigator.clipboard.writeText(JSON.stringify(diagnostics, null, 2));
                    alert('Diagnostics copied to clipboard!');
                  }}
                >
                  <Download className="w-4 h-4" />
                  Export Diagnostics
                </button>
                <button 
                  className="group bg-colors-bg03 hover:bg-obol-light-blue/20 text-obol-text-muted hover:text-obol-light-blue font-medium gap-2 flex items-center transition-all duration-300 px-4 py-2 rounded-xl border border-colors-bg02 hover:border-obol-light-blue/30"
                  onClick={() => window.open('http://monitoring.obol.stack', '_blank')}
                >
                  <FileText className="w-4 h-4" />
                  View Logs
                </button>
              </div>
              <div className="flex gap-3">
                <button 
                  className="group bg-colors-bg03 hover:bg-obol-purple/20 text-obol-text-muted hover:text-obol-purple font-medium gap-2 flex items-center transition-all duration-300 px-4 py-2 rounded-xl border border-colors-bg02 hover:border-obol-purple/30"
                  onClick={() => window.open('https://docs.obol.org', '_blank')}
                >
                  <ExternalLink className="w-4 h-4" />
                  Docs
                </button>
                <button 
                  className="group bg-colors-bg03 hover:bg-obol-orange/20 text-obol-text-muted hover:text-obol-orange font-medium gap-2 flex items-center transition-all duration-300 px-4 py-2 rounded-xl border border-colors-bg02 hover:border-obol-orange/30"
                  onClick={() => window.open('https://github.com/ObolNetwork', '_blank')}
                >
                  <ExternalLink className="w-4 h-4" />
                  GitHub
                </button>
                <button 
                  className="group bg-colors-bg03 hover:bg-obol-green/20 text-obol-text-muted hover:text-obol-green font-medium gap-2 flex items-center transition-all duration-300 px-4 py-2 rounded-xl border border-colors-bg02 hover:border-obol-green/30"
                  onClick={() => window.open('https://discord.gg/obol', '_blank')}
                >
                  <ExternalLink className="w-4 h-4" />
                  Discord
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}