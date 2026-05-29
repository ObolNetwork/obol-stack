'use client';

import type { ReactNode } from 'react';
import { styled } from '@obolnetwork/obol-ui';

import {
  isObolSymbol,
  obolAmountToUsdc,
  useTokenPrices,
} from '@/hooks/useTokenPrices';

export interface ObolAmountSwitchProps {
  amount: string | number;
  symbol?: string;
  unit?: ReactNode;
  prefix?: ReactNode;
  placeholder?: ReactNode;
  variant?: 'inline' | 'rail';
  align?: 'left' | 'right';
  showSymbol?: boolean;
  className?: string;
}

const Root = styled('span', {
  display: 'inline-grid',
  gridTemplateAreas: '"stack"',
  alignItems: 'baseline',
  maxWidth: '100%',
  minWidth: 0,
  verticalAlign: 'baseline',

  '&[data-switchable="true"]': {
    cursor: 'help',
  },

  '&[data-switchable="true"]:focus-visible': {
    outline: '1px solid $obolGreen',
    outlineOffset: 2,
    borderRadius: 4,
  },

  '& [data-face]': {
    gridArea: 'stack',
    minWidth: 0,
    transition: 'opacity 120ms ease',
  },

  '& [data-face="converted"]': {
    opacity: 0,
    pointerEvents: 'none',
  },

  '&[data-switchable="true"]:hover [data-face="primary"], &[data-switchable="true"]:focus-visible [data-face="primary"]':
    {
      opacity: 0,
    },

  '&[data-switchable="true"]:hover [data-face="converted"], &[data-switchable="true"]:focus-visible [data-face="converted"]':
    {
      opacity: 1,
    },

  variants: {
    variant: {
      inline: {
        '& [data-face]': {
          display: 'inline-flex',
          alignItems: 'baseline',
          gap: 4,
          whiteSpace: 'nowrap',
        },
        '& [data-part="amount"], & [data-part="converted-amount"]': {
          fontFamily: 'monospace',
        },
      },
      rail: {
        width: '100%',
        '& [data-face]': {
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'baseline',
          whiteSpace: 'normal',
        },
        '& [data-part="amount"], & [data-part="converted-amount"]': {
          fontFamily: 'monospace',
          color: '$light',
          fontSize: '$7',
          fontWeight: '$semibold',
          letterSpacing: 0,
        },
        '& [data-face="converted"] [data-part="converted-amount"]': {
          fontSize: '$6',
        },
        '& [data-part="symbol"]': {
          fontSize: '$2',
          color: '$body',
          marginLeft: 6,
        },
        '& [data-part="unit"]': {
          flexBasis: '100%',
          marginTop: 2,
          fontFamily: 'monospace',
          fontSize: '$1',
          color: '$muted',
        },
      },
    },
    align: {
      left: {
        justifyItems: 'start',
        textAlign: 'left',
      },
      right: {
        justifyItems: 'end',
        textAlign: 'right',
      },
    },
  },

  defaultVariants: {
    variant: 'inline',
    align: 'left',
  },
});

function plainText(value: ReactNode): string {
  return typeof value === 'string' || typeof value === 'number'
    ? String(value)
    : '';
}

export function ObolAmountSwitch({
  amount,
  symbol,
  unit,
  prefix,
  placeholder = '—',
  variant = 'inline',
  align = 'left',
  showSymbol = true,
  className,
}: ObolAmountSwitchProps) {
  const normalizedSymbol = symbol?.trim() ?? '';
  const amountText = String(amount).trim();
  const hasAmount =
    amountText !== '' && amountText !== '—' && amountText !== '-';
  const isObol = hasAmount && isObolSymbol(normalizedSymbol);
  const { obolPrice } = useTokenPrices({ enabled: isObol });
  const converted = isObol ? obolAmountToUsdc(amountText, obolPrice) : null;
  const switchable = converted !== null;
  const prefixText = plainText(prefix);
  const prefixLabel = prefixText ? `${prefixText} ` : '';
  const unitText = plainText(unit);
  const symbolText = normalizedSymbol || 'token';
  const primaryLabel = `${prefixLabel}${amountText} ${symbolText}${unitText ? ` ${unitText}` : ''}`;
  const convertedLabel = switchable
    ? `${prefixLabel}${converted} USDC${unitText ? ` ${unitText}` : ''}`
    : '';

  if (!hasAmount) {
    return (
      <Root variant={variant} align={align} className={className}>
        <span data-face="primary">{placeholder}</span>
      </Root>
    );
  }

  return (
    <Root
      variant={variant}
      align={align}
      className={className}
      data-switchable={switchable ? 'true' : 'false'}
      tabIndex={switchable ? 0 : undefined}
      title={switchable ? convertedLabel : undefined}
      aria-label={switchable ? `${primaryLabel}; ${convertedLabel}` : undefined}
    >
      <span data-face="primary">
        {prefix ? <span data-part="prefix">{prefix}</span> : null}
        <span data-part="amount">{amountText}</span>
        {showSymbol && normalizedSymbol ? (
          <span data-part="symbol">{normalizedSymbol}</span>
        ) : null}
        {unit ? <span data-part="unit">{unit}</span> : null}
      </span>
      {switchable ? (
        <span data-face="converted" aria-hidden="true">
          {prefix ? <span data-part="prefix">{prefix}</span> : null}
          <span data-part="converted-amount">{converted}</span>
          <span data-part="symbol">USDC</span>
          {unit ? <span data-part="unit">{unit}</span> : null}
        </span>
      ) : null}
    </Root>
  );
}
