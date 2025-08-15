import React, { useState, useEffect } from 'react';
import { Card } from '../shared/Card';
import { Badge } from '../shared/Badge';
import { ProgressBar } from '../shared/ProgressBar';
import { useUptime } from '../shared/useUptime';
import { randomWalkStep, RandomWalkConfig } from '../shared/randomWalkUtils';
import { useTiming } from '../shared/TimingContext';
import { DashboardData } from '../../types/dashboard';

interface NodeOverviewProps {
  data: DashboardData['nodeOverview'];
  ports: DashboardData['ports'];
}

export const NodeOverview: React.FC<NodeOverviewProps> = ({ data, ports }) => {
  // Use live uptime counter starting at 69d 4h 20m
  const uptime = useUptime(69, 4, 20);

  // Random walk states for memory and CPU
  const [memoryValue, setMemoryValue] = useState(6.2); // 6.2GB ± 0.1GB
  const [cpuPercentage, setCpuPercentage] = useState(19); // 19% ± 2%
  
  const { peerTick, isActive } = useTiming();

  useEffect(() => {
    if (isActive) {
      // Update memory usage (6.2GB ± 0.1GB)
      setMemoryValue(prev => {
        const config: RandomWalkConfig = {
          target: 6.2,
          standardDeviation: 0.1,
          minValue: 5.8, // Reasonable lower bound
          maxValue: 6.6, // Reasonable upper bound  
          precision: 1, // 1 decimal place for GB
          meanReversionStrength: 0.25,
          varianceScale: 0.15
        };
        return randomWalkStep(prev, config);
      });

      // Update CPU percentage (19% ± 2%)
      setCpuPercentage(prev => {
        const config: RandomWalkConfig = {
          target: 19,
          standardDeviation: 2,
          minValue: 12, // Reasonable lower bound
          maxValue: 26, // Reasonable upper bound
          precision: 1, // 1 decimal place for percentage
          meanReversionStrength: 0.25,
          varianceScale: 0.15
        };
        return randomWalkStep(prev, config);
      });
    }
  }, [peerTick, isActive]);

  // Calculate dynamic percentages
  const memoryPercentage = (memoryValue / data.memoryUsage.total) * 100;
  const diskPercentage = data.diskUsage.percentage; // Keep disk static

  // Port data for the ports section
  const portList = [
    { key: 'p2p', name: 'P2P', port: ports.p2p.port, status: ports.p2p.status },
    { key: 'jsonRpc', name: 'JSON-RPC', port: ports.jsonRpc.port, status: ports.jsonRpc.status },
    { key: 'prometheus', name: 'Prometheus', port: ports.prometheus.port, status: ports.prometheus.status },
    { key: 'beaconApi', name: 'Beacon API', port: ports.beaconApi.port, status: ports.beaconApi.status },
  ];

  return (
    <Card title="Node Overview">
      {/* System Resources Section */}
      <div className="space-y-4">
        <ProgressBar
          label="Disk"
          value={diskPercentage}
          valueText={`${data.diskUsage.value} ${data.diskUsage.unit} of ${data.diskUsage.total} ${data.diskUsage.unit}`}
          showPercentage
          compact
        />
        
        <ProgressBar
          label="Memory"
          value={memoryPercentage}
          valueText={`${memoryValue} ${data.memoryUsage.unit} of ${data.memoryUsage.total} ${data.memoryUsage.unit}`}
          showPercentage
          compact
        />
        
        <ProgressBar
          label="CPU"
          value={cpuPercentage}
          valueText={`${cpuPercentage}% load average`}
          showPercentage
          compact
        />

        <div className="flex items-center justify-between">
          <span className="text-xs tracking-wide text-zinc-400 uppercase">Uptime</span>
          <span className="text-sm text-zinc-200 font-mono">{uptime.formatted}</span>
        </div>
      </div>

      {/* Ports Section */}
      <div className="mt-6 pt-6 border-t border-white/5">
        <h3 className="text-sm font-medium text-zinc-100 mb-4">Network Ports</h3>
        <div className="divide-y divide-white/5">
          {portList.map((port) => (
            <div key={port.key} className="flex items-center justify-between py-3 first:pt-0 last:pb-0">
              <span className="text-sm font-medium text-zinc-100">{port.name}</span>
              <div className="flex items-center gap-3">
                <span className="text-sm font-mono text-zinc-400">{port.port}</span>
                <Badge 
                  variant={port.status === 'open' ? 'success' : 'error'} 
                  size="sm"
                >
                  {port.status === 'open' ? 'Open' : 'Closed'}
                </Badge>
              </div>
            </div>
          ))}
        </div>
      </div>
    </Card>
  );
};