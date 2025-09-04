'use client';

import React from 'react';

interface ProgressSweepProps {
  duration?: number; // Duration in milliseconds
  className?: string;
  color?: string;
}

export const ProgressSweep: React.FC<ProgressSweepProps> = ({
  duration = 12000,
  className = '',
  color = 'var(--obol-green)',
}) => {
  // Firefox-compatible color values
  const baseColor = color === 'var(--obol-green)' ? '#2FE4AB' : color;

  return (
    <div
      className={`relative w-full bg-zinc-800/50 rounded-full overflow-hidden ${className}`}
      style={{ height: '4px', minHeight: '4px' }} // Explicit height for Firefox
    >
      {/* Progress bar background */}
      <div
        className="absolute inset-0 rounded-full"
        style={{
          background:
            'linear-gradient(90deg, rgba(113, 113, 122, 0.2) 0%, rgba(82, 82, 91, 0.3) 100%)',
        }}
      />

      {/* Animated progress fill - fills left to right */}
      <div
        className="absolute left-0 top-0 bottom-0 rounded-full progress-fill"
        style={{
          background: `linear-gradient(90deg, 
            ${baseColor} 0%, 
            ${baseColor}CC 50%, 
            ${baseColor} 100%)`,
          animationDuration: `${duration}ms`,
          boxShadow: `0 0 8px ${baseColor}66, inset 0 1px 0 ${baseColor}33`,
          width: '0%', // Start at 0% width for animation
        }}
      />

      {/* Inner glow effect */}
      <div
        className="absolute left-0 top-0 bottom-0 rounded-full progress-glow"
        style={{
          background: `linear-gradient(90deg, 
            ${baseColor}80 0%, 
            ${baseColor}40 50%, 
            ${baseColor}20 100%)`,
          filter: 'blur(1px)',
          animationDuration: `${duration}ms`,
          width: '0%', // Start at 0% width for animation
        }}
      />
    </div>
  );
};
