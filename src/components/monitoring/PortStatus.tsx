import React from 'react';
import { Card } from '../shared/Card';
import { Badge } from '../shared/Badge';
import { DashboardData } from '../../types/dashboard';

interface PortStatusProps {
  data: DashboardData['ports'];
}

export const PortStatus: React.FC<PortStatusProps> = ({ data }) => {
  const portList = [
    { key: 'p2p', name: 'P2P', port: data.p2p.port, status: data.p2p.status },
    { key: 'jsonRpc', name: 'JSON-RPC', port: data.jsonRpc.port, status: data.jsonRpc.status },
    { key: 'prometheus', name: 'Prometheus', port: data.prometheus.port, status: data.prometheus.status },
    { key: 'beaconApi', name: 'Beacon API', port: data.beaconApi.port, status: data.beaconApi.status },
  ];

  return (
    <Card title="Ports">
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
    </Card>
  );
};