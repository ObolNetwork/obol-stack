import React from 'react';

export interface PillProps {
  tone?: 'neutral' | 'success' | 'warning' | 'danger';
  children: React.ReactNode;
  className?: string;
}

export function Pill({
  tone = 'neutral',
  children,
  className = '',
}: PillProps) {
  const tones = {
    neutral: 'bg-muted text-gray-200 border-stroke',
    success:
      'bg-[rgba(43,227,154,.10)] text-accent border-[rgba(43,227,154,.25)]',
    warning: 'bg-[rgba(245,166,35,.12)] text-[#f5a623] border-[#f5a623]/30',
    danger: 'bg-[rgba(235,87,87,.12)] text-[#eb5757] border-[#eb5757]/30',
  } as const;

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[11px] font-medium ${tones[tone]} ${className}`}
    >
      <span className="size-1.5 rounded-full bg-current opacity-80" />
      {children}
    </span>
  );
}
