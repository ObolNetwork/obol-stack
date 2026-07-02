import { styled } from '@obolnetwork/obol-ui';
import { ChainName } from '../../../types/dashboard';
import { Button } from '@/components/atoms/Button';
import { tones } from '../../../lib/ui/tones';

export type ChainOption = ChainName;

export interface ChainSelectorProps {
  value: ChainName | undefined;
  onChange: (value: ChainName | undefined) => void;
  /** Allow clicking the active option to deselect (for use as a filter) */
  allowDeselect?: boolean;
  disabledValues?: readonly ChainName[];
  disabledReason?: string;
}

const CHAINS: { value: ChainName; label: string }[] = [
  { value: 'base-sepolia', label: 'Base Sepolia' },
  { value: 'base', label: 'Base Mainnet' },
  { value: 'mainnet', label: 'Ethereum' },
];

const Container = styled('div', {
  display: 'inline-flex',
  borderRadius: '$pill',
  border: '1px solid $stroke',
  background: '$bg02',
  padding: 2,
  gap: '$xs',
});

const Option = styled(Button, {
  variants: {
    active: {
      true: {
        background: tones.success.bg,
        borderColor: tones.success.border,
        color: tones.success.text,
        '&:hover:not(:disabled)': {
          color: tones.success.text,
          borderColor: tones.success.border,
        },
      },
    },
  },
});

export function ChainSelector({
  value,
  onChange,
  allowDeselect = false,
  disabledValues = [],
  disabledReason,
}: ChainSelectorProps) {
  const handleClick = (chain: ChainName) => {
    if (disabledValues.includes(chain)) return;
    if (allowDeselect && value === chain) {
      onChange(undefined);
    } else {
      onChange(chain);
    }
  };

  return (
    <Container>
      {CHAINS.map(chain => {
        const disabled = disabledValues.includes(chain.value);
        return (
          <Option
            key={chain.value}
            type="button"
            variant="ghost"
            size="sm"
            pill
            active={value === chain.value}
            disabled={disabled}
            title={disabled ? disabledReason : undefined}
            onClick={() => handleClick(chain.value)}
          >
            {chain.label}
          </Option>
        );
      })}
    </Container>
  );
}
