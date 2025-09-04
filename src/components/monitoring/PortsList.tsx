import React from 'react';
import { Card } from '../shared/Card';
import { Pill } from '../shared/Pill';
import { DashboardData } from '../../types/dashboard';

interface PortsListProps {
  ports: DashboardData['ports'];
}

interface PortEntry {
  name: string;
  port: number;
  status: 'open' | 'closed';
}

export const PortsList: React.FC<PortsListProps> = ({ ports }) => {
  const portEntries: PortEntry[] = [
    { name: 'P2P', port: ports.p2p.port, status: ports.p2p.status },
    {
      name: 'JSON-RPC',
      port: ports.jsonRpc.port,
      status: ports.jsonRpc.status,
    },
    {
      name: 'Prometheus',
      port: ports.prometheus.port,
      status: ports.prometheus.status,
    },
    {
      name: 'Beacon API',
      port: ports.beaconApi.port,
      status: ports.beaconApi.status,
    },
  ];

  return (
    <Card title="Ports" className="shadow-soft">
      <div className="space-y-0">
        {portEntries.map((port, index) => (
          <div
            key={port.name}
            className={`flex items-center justify-between py-3 ${
              index > 0 ? 'border-t border-stroke/60' : ''
            }`}
          >
            <span className="font-medium text-white">{port.name}</span>
            <div className="flex items-center gap-3">
              <span className="font-mono text-sm text-gray-300 tabular-nums">
                {port.port}
              </span>
              <Pill tone={port.status === 'open' ? 'success' : 'danger'}>
                {port.status === 'open' ? 'Open' : 'Closed'}
              </Pill>
            </div>
          </div>
        ))}
      </div>
    </Card>
  );
};
