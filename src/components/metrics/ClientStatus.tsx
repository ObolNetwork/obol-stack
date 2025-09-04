import React from 'react';
import { Card } from '../shared/Card';
import { Badge } from '../shared/Badge';
import { RandomWalkNumber } from '../shared/RandomWalkNumber';
import { DashboardData } from '../../types/dashboard';

interface ClientStatusProps {
  client: 'nethermind' | 'nimbus';
  data:
    | DashboardData['clients']['nethermind']
    | DashboardData['clients']['nimbus'];
}

export const ClientStatus: React.FC<ClientStatusProps> = ({ client, data }) => {
  const clientConfig = {
    nethermind: {
      name: 'Nethermind',
      role: 'Execution',
      icon: '/nethermind-icon.png',
      avatarBg: 'bg-blue-600',
    },
    nimbus: {
      name: 'Nimbus',
      role: 'Consensus',
      icon: '/nimbus-icon.png',
      avatarBg: 'bg-orange-600',
    },
  };

  const config = clientConfig[client];

  return (
    <Card className="p-5">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-3">
          <div
            className={`w-8 h-8 rounded-lg ${config.avatarBg} bg-white/10`}
            style={{
              backgroundImage: `url(${config.icon})`,
              backgroundSize: 'cover',
              backgroundPosition: 'center',
              backgroundRepeat: 'no-repeat',
            }}
          />
          <div>
            <div className="font-semibold text-zinc-100">{config.name}</div>
            <div className="text-sm text-zinc-400">{config.role}</div>
          </div>
        </div>
        <Badge variant="success" size="sm">
          Synced
        </Badge>
      </div>

      <div className="space-y-2">
        <div className="flex justify-between text-sm">
          <span className="text-zinc-400">Version</span>
          <span className="text-zinc-200 font-mono">{data.version}</span>
        </div>
        <div className="flex justify-between text-sm">
          <span className="text-zinc-400">Peers</span>
          <span className="text-zinc-200 font-mono">
            <RandomWalkNumber
              initialValue={data.peers}
              target={72}
              standardDeviation={10}
              updateInterval={4000}
              animateLastDigit={true}
            />
          </span>
        </div>
      </div>
    </Card>
  );
};
