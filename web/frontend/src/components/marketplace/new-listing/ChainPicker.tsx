'use client';

import { styled } from '@obolnetwork/obol-ui';
import { Button } from '@/components/atoms/Button';
import { ChainIcon } from '@/components/atoms/ChainIcon';
import type { SellPaymentChain } from '@/lib/services/sell-assets';
import type { ChainName } from '@/types/dashboard';

export type ChainKey = SellPaymentChain;

export const CHAIN_OPTIONS: Array<{
  k: ChainKey;
  label: string;
  icon: ChainName;
}> = [
  { k: 'base', label: 'Base', icon: 'base' },
  { k: 'base-sepolia', label: 'Base Sepolia', icon: 'base-sepolia' },
  { k: 'mainnet', label: 'Mainnet', icon: 'mainnet' },
];

const ChainGrid = styled('div', {
  display: 'grid',
  gridTemplateColumns: 'repeat(3, 1fr)',
  gap: 8,
});

// Chain option — Button atom (secondary/md) carrying only the active state.
// Disabled chains use the atom's built-in :disabled treatment.
const ChainCard = styled(Button, {
  justifyContent: 'flex-start',
  textAlign: 'left',

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        borderColor: '$obolGreen',
        color: '$obolGreen',
        '&:hover:not(:disabled)': {
          background: '$obolMidGreen',
          borderColor: '$obolGreen',
        },
      },
    },
  },

  defaultVariants: {
    variant: 'secondary',
    size: 'md',
  },
});

export interface ChainPickerProps {
  value: ChainKey;
  onChange: (k: ChainKey) => void;
  disabledChains?: readonly ChainKey[];
  disabledReason?: string;
}

export function ChainPicker({
  value,
  onChange,
  disabledChains = [],
  disabledReason,
}: ChainPickerProps) {
  return (
    <ChainGrid>
      {CHAIN_OPTIONS.map(opt => {
        const disabled = disabledChains.includes(opt.k);
        return (
          <ChainCard
            key={opt.k}
            type="button"
            active={opt.k === value}
            disabled={disabled}
            title={disabled ? disabledReason : undefined}
            onClick={() => {
              if (!disabled) onChange(opt.k);
            }}
          >
            <ChainIcon chain={opt.icon} size={16} />
            <span style={{ flex: 1 }}>{opt.label}</span>
          </ChainCard>
        );
      })}
    </ChainGrid>
  );
}
