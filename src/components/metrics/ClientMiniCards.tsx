import React from 'react';
import { Card } from '../shared/Card';
import { Pill } from '../shared/Pill';
import { formatVersion } from '../../lib/format';
import { DashboardData } from '../../types/dashboard';

interface ClientMiniCardsProps {
  clients: DashboardData['clients'];
}

interface ClientMiniCardProps {
  name: string;
  role: string;
  version: string;
  peers: number;
  status: 'synced' | 'syncing' | 'error';
  icon: React.ReactNode;
}

function ClientMiniCard({ name, role, version, peers, status, icon }: ClientMiniCardProps) {
  const getStatusTone = (status: string): 'success' | 'warning' | 'danger' => {
    switch (status) {
      case 'synced': return 'success';
      case 'syncing': return 'warning';
      case 'error': return 'danger';
      default: return 'danger';
    }
  };

  const getStatusText = (status: string): string => {
    switch (status) {
      case 'synced': return 'Synced';
      case 'syncing': return 'Syncing';
      case 'error': return 'Error';
      default: return 'Unknown';
    }
  };

  return (
    <Card className="p-3 shadow-soft">
      <div className="flex items-start justify-between mb-2">
        <div className="flex items-center gap-2">
          <div className="w-6 h-6 flex items-center justify-center flex-shrink-0">
            {icon}
          </div>
          <div>
            <div className="font-medium text-white text-sm">{name}</div>
            <div className="text-[11px] text-gray-400">{role}</div>
          </div>
        </div>
        <Pill tone={getStatusTone(status)}>
          {getStatusText(status)}
        </Pill>
      </div>
      
      <div className="space-y-1 text-[11px]">
        <div className="flex justify-between">
          <span className="text-gray-400">Version</span>
          <span className="text-gray-200 font-mono">{formatVersion(version)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-gray-400">Peers</span>
          <span className="text-gray-200 font-mono">{peers}</span>
        </div>
      </div>
    </Card>
  );
}

export const ClientMiniCards: React.FC<ClientMiniCardsProps> = ({ clients }) => {
  return (
    <div className="grid grid-cols-2 gap-4">
      <ClientMiniCard 
        name="Nethermind"
        role="Execution"
        version={clients.nethermind.version}
        peers={clients.nethermind.peers}
        status={clients.nethermind.status}
        icon={
          <div 
            className="w-6 h-6 rounded bg-obol-blue"
            style={{
              backgroundImage: 'url(/nethermind-icon.png)',
              backgroundSize: 'cover',
              backgroundPosition: 'center',
              backgroundRepeat: 'no-repeat'
            }}
          />
        }
      />
      <ClientMiniCard 
        name="Nimbus"
        role="Consensus"
        version={clients.nimbus.version}
        peers={clients.nimbus.peers}
        status={clients.nimbus.status}
        icon={
          <div 
            className="w-6 h-6 rounded bg-orange-600"
            style={{
              backgroundImage: 'url(/nimbus-icon.png)',
              backgroundSize: 'cover',
              backgroundPosition: 'center',
              backgroundRepeat: 'no-repeat'
            }}
          />
        }
      />
    </div>
  );
};