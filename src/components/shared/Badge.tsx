import React from 'react';

export interface BadgeProps {
  children: React.ReactNode;
  variant?: 'primary' | 'neutral' | 'success' | 'warning' | 'error';
  size?: 'sm' | 'md';
  className?: string;
}

export function Badge({
  children,
  variant = 'neutral',
  size = 'md',
  className = '',
}: BadgeProps) {
  const variantClasses = {
    primary: 'bg-emerald-500/15 text-emerald-300 border-emerald-400/20',
    neutral: 'bg-white/5 text-zinc-300 border-white/10',
    success: 'bg-emerald-500/15 text-emerald-300 border-emerald-400/20',
    warning: 'bg-amber-500/15 text-amber-300 border-amber-400/20',
    error: 'bg-red-500/15 text-red-300 border-red-400/20',
  };

  const sizeClasses = {
    sm: 'px-2 py-0.5 text-xs',
    md: 'px-3 py-1 text-sm',
  };

  return (
    <span
      className={`
        inline-flex items-center gap-1.5 rounded-full border
        ${variantClasses[variant]}
        ${sizeClasses[size]}
        font-medium transition-colors
        ${className}
      `}
    >
      {variant === 'success' && (
        <span className="block size-2 rounded-full bg-emerald-400" />
      )}
      {children}
    </span>
  );
}
