import React from 'react';
import { Globe } from 'lucide-react';
import { Card } from '../shared/Card';
import { Pill } from '../shared/Pill';
import {
  MetricItem,
  InlineMetaItem,
  InlineMetaSeparator,
} from '../shared/MetricItem';
import { formatNumber } from '../../lib/format';
import { DashboardData } from '../../types/dashboard';

interface EthereumSummaryCardProps {
  data: DashboardData['ethereum'];
}

export const EthereumSummaryCard: React.FC<EthereumSummaryCardProps> = ({
  data,
}) => {
  const getStatusTone = (status: string): 'success' | 'warning' | 'danger' => {
    switch (status) {
      case 'synced':
        return 'success';
      case 'syncing':
        return 'warning';
      case 'error':
        return 'danger';
      default:
        return 'danger';
    }
  };

  const getStatusText = (status: string): string => {
    switch (status) {
      case 'synced':
        return 'Synced';
      case 'syncing':
        return 'Syncing';
      case 'error':
        return 'Error';
      default:
        return 'Unknown';
    }
  };

  return (
    <Card className="shadow-soft">
      {/* Title Row */}
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-3">
          <div className="w-6 h-6 rounded-full bg-obol-blue flex items-center justify-center flex-shrink-0">
            <Globe className="w-3 h-3 text-white" />
          </div>
          <span className="font-semibold text-white">Ethereum Mainnet</span>
        </div>
        <Pill tone={getStatusTone(data.status)}>
          {getStatusText(data.status)}
        </Pill>
      </div>

      {/* Inline Meta Strip */}
      <div className="flex items-center gap-1 text-[11px] mb-4 flex-wrap">
        <InlineMetaItem label="Block" value={formatNumber(data.blocks)} />
        <InlineMetaSeparator />
        <InlineMetaItem label="Headslot" value={formatNumber(data.finalized)} />
        <InlineMetaSeparator />
        <InlineMetaItem label="Distance to justify" value={data.distance} />
        <InlineMetaSeparator />
        <InlineMetaItem label="Peers" value={data.clients} />
      </div>

      {/* Compact Metrics Row */}
      <div className="grid grid-cols-4 gap-4">
        <MetricItem label="Finalized" value={formatNumber(data.finalized)} />
        <MetricItem label="Block Height" value={formatNumber(data.blocks)} />
        <MetricItem label="Node Mode" value="Full" />
        <MetricItem
          label="Participation"
          value={`${data.participation}%`}
          accent
        />
      </div>
    </Card>
  );
};
