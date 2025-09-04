import React from 'react';
import { Card } from '../shared/Card';
import { Badge } from '../shared/Badge';
import { AnimatedNumber } from '../shared/AnimatedNumber';
import { ProgressSweep } from '../shared/ProgressSweep';
import { ParticipationNumber } from '../shared/ParticipationNumber';
import { RandomWalkNumber } from '../shared/RandomWalkNumber';
import { DashboardData } from '../../types/dashboard';

interface NetworkStatusProps {
  data: DashboardData['ethereum'];
  clients: DashboardData['clients'];
}

export const NetworkStatus: React.FC<NetworkStatusProps> = ({
  data,
  clients,
}) => {
  // Client configurations
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
  return (
    <Card className="p-6 md:p-7 relative overflow-hidden">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-lg font-semibold text-zinc-100">
          Ethereum Mainnet
        </h2>
        <Badge variant="success" size="sm">
          Synced
        </Badge>
      </div>

      {/* Metrics grid - Clean 2x2 layout */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <div>
          <div className="text-xs tracking-wide text-zinc-400 uppercase mb-1">
            BLOCK
          </div>
          <div className="text-xl md:text-2xl font-semibold text-zinc-100 font-mono">
            <AnimatedNumber
              value={data.blocks}
              animateLastDigit={true}
              incrementInterval={12000}
            />
          </div>
        </div>
        <div>
          <div className="text-xs tracking-wide text-zinc-400 uppercase mb-1">
            FINALIZED EPOCH
          </div>
          <div className="text-xl md:text-2xl font-semibold text-zinc-100 font-mono">
            <AnimatedNumber
              value={Math.floor(data.blocks / 32) - 2}
              animateLastDigit={true}
              incrementInterval={384000}
            />
          </div>
        </div>
        <div>
          <div className="text-xs tracking-wide text-zinc-400 uppercase mb-1">
            NODE MODE
          </div>
          <div className="text-xl md:text-2xl font-semibold text-zinc-100">
            Full
          </div>
        </div>
        <div>
          <div className="text-xs tracking-wide text-zinc-400 uppercase mb-1">
            PARTICIPATION
          </div>
          <div className="text-xl md:text-2xl font-semibold font-mono">
            <ParticipationNumber
              initialValue={data.participation}
              target={97.5}
              standardDeviation={1}
              updateInterval={4000}
            />
          </div>
        </div>
      </div>

      {/* Embedded Client Components */}
      <div className="mt-6 pt-6 border-t border-white/5">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          {/* Nethermind Client */}
          <div className="bg-white/5 rounded-lg p-4">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-3">
                <div
                  className={`w-8 h-8 rounded-lg ${clientConfig.nethermind.avatarBg} bg-white/10`}
                  style={{
                    backgroundImage: `url(${clientConfig.nethermind.icon})`,
                    backgroundSize: 'cover',
                    backgroundPosition: 'center',
                    backgroundRepeat: 'no-repeat',
                  }}
                />
                <div>
                  <div className="font-semibold text-zinc-100">
                    {clientConfig.nethermind.name}
                  </div>
                  <div className="text-sm text-zinc-400">
                    {clientConfig.nethermind.role}
                  </div>
                </div>
              </div>
              <Badge variant="success" size="sm">
                Synced
              </Badge>
            </div>
            <div className="space-y-2">
              <div className="flex justify-between text-sm">
                <span className="text-zinc-400">Version</span>
                <span className="text-zinc-200 font-mono">
                  {clients.nethermind.version}
                </span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-zinc-400">Peers</span>
                <span className="text-zinc-200 font-mono">
                  <RandomWalkNumber
                    initialValue={clients.nethermind.peers}
                    target={72}
                    standardDeviation={10}
                    updateInterval={4000}
                    animateLastDigit={true}
                  />
                </span>
              </div>
            </div>
          </div>

          {/* Nimbus Client */}
          <div className="bg-white/5 rounded-lg p-4">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-3">
                <div
                  className={`w-8 h-8 rounded-lg ${clientConfig.nimbus.avatarBg} bg-white/10`}
                  style={{
                    backgroundImage: `url(${clientConfig.nimbus.icon})`,
                    backgroundSize: 'cover',
                    backgroundPosition: 'center',
                    backgroundRepeat: 'no-repeat',
                  }}
                />
                <div>
                  <div className="font-semibold text-zinc-100">
                    {clientConfig.nimbus.name}
                  </div>
                  <div className="text-sm text-zinc-400">
                    {clientConfig.nimbus.role}
                  </div>
                </div>
              </div>
              <Badge variant="success" size="sm">
                Synced
              </Badge>
            </div>
            <div className="space-y-2">
              <div className="flex justify-between text-sm">
                <span className="text-zinc-400">Version</span>
                <span className="text-zinc-200 font-mono">
                  {clients.nimbus.version}
                </span>
              </div>
              <div className="flex justify-between text-sm">
                <span className="text-zinc-400">Peers</span>
                <span className="text-zinc-200 font-mono">
                  <RandomWalkNumber
                    initialValue={clients.nimbus.peers}
                    target={72}
                    standardDeviation={10}
                    updateInterval={4000}
                    animateLastDigit={true}
                  />
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Progress sweep bar - positioned at bottom of card */}
      <div className="absolute bottom-0 left-0 right-0">
        <ProgressSweep duration={12000} className="rounded-none" />
      </div>
    </Card>
  );
};
