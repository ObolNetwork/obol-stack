import React from 'react';

export interface CardProps {
  children: React.ReactNode;
  className?: string;
  title?: string;
  hover?: boolean;
}

export function Card({
  children,
  className = '',
  title,
  hover = true,
}: CardProps) {
  return (
    <div
      className={`
        rounded-2xl bg-zinc-900/60 backdrop-blur border border-white/10 p-6
        ${hover ? 'hover:bg-white/5 transition-colors duration-300' : ''}
        ${className}
      `}
    >
      {title && <h2 className="font-semibold text-zinc-100 mb-4">{title}</h2>}
      {children}
    </div>
  );
}
