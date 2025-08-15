import React from 'react';

interface ProgressBarProps {
  value: number; // 0-100
  label?: string;
  valueText?: string;
  variant?: 'success' | 'warning' | 'error';
  showPercentage?: boolean;
  compact?: boolean;
}

export const ProgressBar: React.FC<ProgressBarProps> = ({ 
  value, 
  label,
  valueText,
  variant, 
  showPercentage = false,
  compact = false 
}) => {
  // Auto-determine variant based on value if not provided
  const getVariant = (val: number) => {
    if (variant) return variant;
    if (val >= 90) return 'error';
    if (val >= 75) return 'warning';
    return 'success';
  };

  const currentVariant = getVariant(value);
  
  const variantClasses = {
    success: 'bg-[var(--obol-green)]',
    warning: 'bg-amber-500', 
    error: 'bg-red-500'
  };

  if (compact) {
    return (
      <div className="flex items-center justify-between gap-3">
        <div className="flex-1">
          {label && (
            <div className="flex justify-between items-center mb-1">
              <span className="text-xs tracking-wide text-zinc-400 uppercase">{label}</span>
              {valueText && (
                <span className="text-sm text-zinc-200">{valueText}</span>
              )}
            </div>
          )}
          <div className="h-2 rounded-full bg-white/10 overflow-hidden">
            <div 
              className={`h-2 rounded-full transition-[width] duration-500 ${variantClasses[currentVariant]}`}
              style={{ width: `${Math.min(Math.max(value, 0), 100)}%` }}
            />
          </div>
        </div>
        {showPercentage && (
          <span className="rounded-full px-2 py-0.5 text-xs bg-emerald-500/10 text-emerald-300 border border-emerald-400/20 ml-auto">
            {Math.round(value)}%
          </span>
        )}
      </div>
    );
  }

  return (
    <div className="w-full">
      {label && (
        <div className="flex justify-between items-center mb-2">
          <span className="text-xs tracking-wide text-zinc-400 uppercase">{label}</span>
          {valueText && (
            <span className="text-sm text-zinc-200">{valueText}</span>
          )}
        </div>
      )}
      <div className="h-2 rounded-full bg-white/10 overflow-hidden">
        <div 
          className={`h-2 rounded-full transition-[width] duration-500 ${variantClasses[currentVariant]}`}
          style={{ width: `${Math.min(Math.max(value, 0), 100)}%` }}
        />
      </div>
      {showPercentage && (
        <div className="text-xs text-zinc-400 mt-1 text-right">
          {value.toFixed(1)}%
        </div>
      )}
    </div>
  );
};