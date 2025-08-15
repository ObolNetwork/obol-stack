import React from 'react';
import Image from 'next/image';
import { Badge } from '../shared/Badge';

interface HeaderProps {
  network: 'testnet' | 'mainnet';
  nodeStatus: 'online' | 'offline' | 'syncing';
}

export const Header: React.FC<HeaderProps> = ({ network }) => {
  return (
    <header className="flex items-center justify-between mb-6">
      <div className="flex items-center gap-3">
        <Image
          src="/stack-icon.svg"
          alt="Obol Stack Logo"
          width={39}
          height={24}
          className="opacity-90"
        />
        <span className="text-lg font-semibold tracking-tight text-zinc-100">Obol Stack</span>
      </div>

      <div className="flex items-center gap-2">
        <Badge variant="neutral">
          {network.charAt(0).toUpperCase() + network.slice(1)}
        </Badge>
        <Badge variant="neutral">Full Mode</Badge>
      </div>
    </header>
  );
};