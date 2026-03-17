import { styled } from '@obolnetwork/obol-ui';
import { ChainName } from '../../../types/dashboard';

export type ChainOption = ChainName;

export interface ChainSelectorProps {
  value: ChainName | undefined;
  onChange: (value: ChainName | undefined) => void;
  /** Allow clicking the active option to deselect (for use as a filter) */
  allowDeselect?: boolean;
}

const CHAINS: { value: ChainName; label: string }[] = [
  { value: 'base-sepolia', label: 'Base Sepolia' },
  { value: 'base', label: 'Base Mainnet' },
];

const Container = styled('div', {
  display: 'inline-flex',
  borderRadius: '$pill',
  border: '1px solid $stroke',
  background: '$bg02',
  padding: 2,
  gap: '$xs',
});

const Option = styled('button', {
  borderRadius: '$pill',
  border: 'none',
  background: 'transparent',
  color: '$muted',
  fontSize: '$1',
  fontWeight: '$medium',
  padding: '$xxxs $xs',
  cursor: 'pointer',
  transition: 'all 0.15s ease',
  lineHeight: 1,
  whiteSpace: 'nowrap',

  variants: {
    active: {
      true: {
        background: 'rgba(98, 126, 234, 0.2)',
        color: '#8fa3f0',
        border: '1px solid rgba(98, 126, 234, 0.35)',
      },
      false: {
        background: '$bg03',
        border: '1px solid $stroke',
        color: '$body',
        '&:hover': {
          background: '$bg04',
          borderColor: '$bg05',
          color: '$light',
        },
      },
    },
  },
});

export function ChainSelector({
  value,
  onChange,
  allowDeselect = false,
}: ChainSelectorProps) {
  const handleClick = (chain: ChainName) => {
    if (allowDeselect && value === chain) {
      onChange(undefined);
    } else {
      onChange(chain);
    }
  };

  return (
    <Container>
      {CHAINS.map(chain => (
        <Option
          key={chain.value}
          type="button"
          active={value === chain.value}
          onClick={() => handleClick(chain.value)}
        >
          {chain.label}
        </Option>
      ))}
    </Container>
  );
}
