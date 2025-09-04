import React from 'react';
import { HardDrive, Cpu, Activity, Clock } from 'lucide-react';
import { Card } from '../shared/Card';
import { ThinProgress } from '../shared/ThinProgress';
import {
  formatFileSize,
  formatUptime,
  formatPercentage,
} from '../../lib/format';
import { DashboardData } from '../../types/dashboard';

interface NodeOverviewCompactProps {
  metrics: DashboardData['nodeOverview'];
}

interface NodeMetricRowProps {
  icon: React.ReactNode;
  label: string;
  value: string;
  percentage?: number;
  isWarning?: boolean;
}

function NodeMetricRow({
  icon,
  label,
  value,
  percentage,
  isWarning = false,
}: NodeMetricRowProps) {
  return (
    <div className="flex items-center justify-between">
      <div className="flex items-center gap-3 flex-1">
        <div className="w-4 h-4 flex items-center justify-center text-gray-400 flex-shrink-0">
          {icon}
        </div>
        <div className="min-w-0">
          <div className="font-medium text-white text-sm">{label}</div>
          <div className="text-[11px] text-gray-400 truncate">{value}</div>
        </div>
      </div>

      {percentage !== undefined && (
        <div className="text-right ml-3 min-w-[60px]">
          <div
            className={`text-xs px-2 py-0.5 rounded mb-1 ${
              isWarning
                ? 'bg-warning/10 text-warning'
                : 'bg-accent/10 text-accent'
            }`}
          >
            {formatPercentage(percentage, 0)}
          </div>
          <ThinProgress
            value={percentage}
            isWarning={isWarning}
            className="w-12"
          />
        </div>
      )}
    </div>
  );
}

export const NodeOverviewCompact: React.FC<NodeOverviewCompactProps> = ({
  metrics,
}) => {
  const diskValue = `${formatFileSize(metrics.diskUsage.value * 1024 * 1024 * 1024)} of ${formatFileSize(metrics.diskUsage.total * 1024 * 1024 * 1024)}`;
  const memoryValue = `${formatFileSize(metrics.memoryUsage.value * 1024 * 1024 * 1024)} of ${formatFileSize(metrics.memoryUsage.total * 1024 * 1024 * 1024)}`;
  const uptimeValue = formatUptime(metrics.uptime.hours);

  return (
    <Card title="Node Overview" className="shadow-soft">
      <div className="space-y-4">
        <NodeMetricRow
          icon={<HardDrive className="w-4 h-4" />}
          label="Disk"
          value={diskValue}
          percentage={metrics.diskUsage.percentage}
        />

        <NodeMetricRow
          icon={<Cpu className="w-4 h-4" />}
          label="Memory"
          value={memoryValue}
          percentage={metrics.memoryUsage.percentage}
        />

        <NodeMetricRow
          icon={<Activity className="w-4 h-4" />}
          label="CPU"
          value={`${formatPercentage(metrics.cpuLoad.percentage, 0)} load average`}
          percentage={metrics.cpuLoad.percentage}
          isWarning={metrics.cpuLoad.percentage >= 90}
        />

        <NodeMetricRow
          icon={<Clock className="w-4 h-4" />}
          label="Uptime"
          value={uptimeValue}
        />
      </div>
    </Card>
  );
};
