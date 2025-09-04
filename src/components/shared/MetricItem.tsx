import React from 'react';

export interface MetricItemProps {
  label: string;
  value: string;
  accent?: boolean;
  className?: string;
}

export function MetricItem({
  label,
  value,
  accent = false,
  className = '',
}: MetricItemProps) {
  return (
    <div className={className}>
      <div className="text-[11px] uppercase tracking-wide text-gray-400 mb-1">
        {label}
      </div>
      <div
        className={`font-semibold tabular-nums tracking-[.02em] ${
          accent ? 'text-accent' : 'text-gray-100'
        }`}
      >
        {value}
      </div>
    </div>
  );
}

export interface InlineMetaItemProps {
  label: string;
  value: string | number;
  className?: string;
}

export function InlineMetaItem({
  label,
  value,
  className = '',
}: InlineMetaItemProps) {
  return (
    <div className={`flex items-center gap-1 text-[11px] ${className}`}>
      <span className="text-gray-400 uppercase tracking-wide">{label}</span>
      <span className="font-semibold text-gray-100">{value}</span>
    </div>
  );
}

export function InlineMetaSeparator() {
  return (
    <span className="text-gray-600 opacity-60 mx-2" aria-hidden="true">
      •
    </span>
  );
}
