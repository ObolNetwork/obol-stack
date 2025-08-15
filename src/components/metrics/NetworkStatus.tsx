import React from 'react';
import { Card } from '../shared/Card';
import { Badge } from '../shared/Badge';
import { AnimatedNumber } from '../shared/AnimatedNumber';
import { ProgressSweep } from '../shared/ProgressSweep';
import { ParticipationNumber } from '../shared/ParticipationNumber';
import { DashboardData } from '../../types/dashboard';

interface NetworkStatusProps {
  data: DashboardData['ethereum'];
}

export const NetworkStatus: React.FC<NetworkStatusProps> = ({ data }) => {
  return (
    <Card className="p-6 md:p-7 relative overflow-hidden">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-lg font-semibold text-zinc-100">Ethereum Mainnet</h2>
        <Badge variant="success" size="sm">
          Synced
        </Badge>
      </div>
      
      {/* Metrics grid - Clean 2x2 layout */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <div>
          <div className="text-xs tracking-wide text-zinc-400 uppercase mb-1">BLOCK</div>
          <div className="text-xl md:text-2xl font-semibold text-zinc-100 font-mono">
            <AnimatedNumber 
              value={data.blocks} 
              animateLastDigit={true}
              incrementInterval={12000}
            />
          </div>
        </div>
        <div>
          <div className="text-xs tracking-wide text-zinc-400 uppercase mb-1">FINALIZED EPOCH</div>
          <div className="text-xl md:text-2xl font-semibold text-zinc-100 font-mono">
            <AnimatedNumber 
              value={Math.floor(data.blocks / 32) - 2}
              animateLastDigit={true}
              incrementInterval={384000}
            />
          </div>
        </div>
        <div>
          <div className="text-xs tracking-wide text-zinc-400 uppercase mb-1">NODE MODE</div>
          <div className="text-xl md:text-2xl font-semibold text-zinc-100">
            Full
          </div>
        </div>
        <div>
          <div className="text-xs tracking-wide text-zinc-400 uppercase mb-1">PARTICIPATION</div>
          <div className="text-xl md:text-2xl font-semibold font-mono">
            <ParticipationNumber
              initialValue={data.participation}
              target={97.5}
              standardDeviation={1}
              updateInterval={4000}
              animateLastDigit={true}
            />
          </div>
        </div>
      </div>

      {/* Progress sweep bar - positioned at bottom of card */}
      <div className="absolute bottom-0 left-0 right-0">
        <ProgressSweep 
          duration={12000}
          className="rounded-none"
        />
      </div>
    </Card>
  );
};