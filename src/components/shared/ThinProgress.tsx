import React from 'react';

export interface ThinProgressProps {
  value: number;
  isWarning?: boolean;
  className?: string;
}

export function ThinProgress({
  value,
  isWarning = false,
  className = '',
}: ThinProgressProps) {
  const clampedValue = Math.min(Math.max(value, 0), 100);

  return (
    <div
      className={`w-full h-[6px] rounded-full bg-muted/60 overflow-hidden mt-1 ${className}`}
    >
      <div
        className={`h-full rounded-full transition-[width] duration-500 ${
          isWarning ? 'bg-warning' : 'bg-accent'
        }`}
        style={{ width: `${clampedValue}%` }}
      />
    </div>
  );
}
