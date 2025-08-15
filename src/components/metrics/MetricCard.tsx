import React from 'react';
import { StatusIndicator } from '../shared/StatusIndicator';
import { ProgressBar } from '../shared/ProgressBar';
import { MetricCardProps } from '../../types/dashboard';

export const MetricCard: React.FC<MetricCardProps> = ({ 
  label, 
  value, 
  unit, 
  percentage, 
  status,
  trend,
  icon 
}) => {
  const getStatusFromPercentage = (pct: number) => {
    if (pct >= 90) return 'error';
    if (pct >= 75) return 'warning';
    return 'success';
  };

  const displayStatus = status || (percentage !== undefined ? getStatusFromPercentage(percentage) : undefined);

  return (
    <div className="bg-colors-cardBackgroundDark rounded-lg border border-colors-bg03 p-4 hover:border-obol-green/30 transition-all duration-300">
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-2">
          {icon && <div className="text-obol-text-muted">{icon}</div>}
          <span className="text-sm text-obol-text-muted font-medium">{label}</span>
        </div>
        {displayStatus && <StatusIndicator status={displayStatus} size="sm" />}
      </div>
      
      <div className="text-xl font-bold text-white mb-1">
        {value}{unit && <span className="text-sm text-obol-text-muted ml-1">{unit}</span>}
      </div>
      
      {percentage !== undefined && (
        <div className="mt-3">
          <ProgressBar 
            value={percentage} 
            variant={displayStatus}
          />
        </div>
      )}
      
      {trend && (
        <div className="mt-2 flex items-center gap-1">
          <div className={`w-2 h-2 rounded-full ${
            trend === 'up' ? 'bg-obol-green' : 
            trend === 'down' ? 'bg-red-500' : 
            'bg-obol-text-muted'
          }`} />
          <span className="text-xs text-obol-text-muted capitalize">{trend}</span>
        </div>
      )}
    </div>
  );
};