import React from 'react';

interface StatusIndicatorProps {
  status: 'success' | 'warning' | 'error' | 'syncing';
  size?: 'sm' | 'md' | 'lg';
  showPulse?: boolean;
}

export const StatusIndicator: React.FC<StatusIndicatorProps> = ({
  status,
  size = 'sm',
  showPulse = false,
}) => {
  const sizeClasses = {
    sm: 'w-2 h-2',
    md: 'w-3 h-3',
    lg: 'w-4 h-4',
  };

  const statusClasses = {
    success: 'bg-obol-green',
    warning: 'bg-obol-orange',
    error: 'bg-red-500',
    syncing: 'bg-obol-light-blue',
  };

  return (
    <div
      className={`
        ${sizeClasses[size]} 
        ${statusClasses[status]} 
        rounded-full 
        ${showPulse || status === 'syncing' ? 'animate-pulse' : ''}
      `}
    />
  );
};
