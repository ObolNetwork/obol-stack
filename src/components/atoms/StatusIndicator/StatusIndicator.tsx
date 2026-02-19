import React from 'react';
import {
  Box,
  Flex,
  Text,
  styled,
  theme,
  keyframes,
} from '@obolnetwork/obol-ui';
import { Tooltip } from '../Tooltip';

export type StatusType = 'synced' | 'syncing' | 'error' | 'unsynced';

export interface StatusIndicatorProps {
  status?: StatusType;
  size?: 'sm' | 'md';
  className?: string;
}

const outerPulse = keyframes({
  '0%, 100%': { opacity: 0.2 },
  '50%': { opacity: 0.5 },
});

const STATUS_CONFIG: Record<
  StatusType,
  { color: string; label: string; pulse: boolean }
> = {
  synced: {
    color: theme.colors.obolGreen.value,
    label: 'Synced',
    pulse: true,
  },
  syncing: {
    color: '#F5A623',
    label: 'Syncing',
    pulse: true,
  },
  error: {
    color: '#FF6B6B',
    label: 'Error',
    pulse: false,
  },
  unsynced: {
    color: theme.colors.body.value,
    label: 'Unsynced',
    pulse: false,
  },
};

const SIZE_VALUES: Record<
  NonNullable<StatusIndicatorProps['size']>,
  { container: number; dot: number }
> = {
  sm: { container: 16, dot: 8 },
  md: { container: 20, dot: 10 },
};

const StatusContainer = styled(Box, {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  position: 'relative',
  flexShrink: 0,
});

const OuterRing = styled(Box, {
  position: 'absolute',
  inset: 0,
  borderRadius: '$round',
});

const InnerDot = styled(Box, {
  borderRadius: '$round',
  flexShrink: 0,
  position: 'relative',
});

const PillContainer = styled(Flex, {
  alignItems: 'center',
  width: 'fit-content',
  gap: '6px',
  borderRadius: '$pill',
  paddingTop: '$xxxs',
  paddingBottom: '$xxxs',
  paddingLeft: '$xs',
  paddingRight: '$xs',
  fontSize: '$1',
  fontWeight: '$medium',
});

export interface StatusPillProps {
  status?: StatusType;
  label?: string;
}

export function StatusPill({ status = 'synced', label }: StatusPillProps) {
  const config = STATUS_CONFIG[status];
  const displayLabel = label ?? config.label;

  return (
    <PillContainer
      css={{ backgroundColor: `${config.color}18`, color: config.color }}
    >
      <StatusIndicator status={status} size="sm" />
      <Text css={{ fontSize: '$1', fontWeight: '$medium', color: 'inherit' }}>
        {displayLabel}
      </Text>
    </PillContainer>
  );
}

export function StatusIndicator({
  status = 'synced',
  size = 'md',
  className,
}: StatusIndicatorProps) {
  const config = STATUS_CONFIG[status];
  const sizes = SIZE_VALUES[size];

  return (
    <Tooltip content={config.label} side="bottom">
      <StatusContainer
        className={className}
        css={{ width: sizes.container, height: sizes.container }}
      >
        <OuterRing
          css={{
            backgroundColor: config.color,
            opacity: 0.25,
            ...(config.pulse
              ? { animation: `${outerPulse} 2s ease-in-out infinite` }
              : {}),
          }}
        />
        <InnerDot
          css={{
            width: sizes.dot,
            height: sizes.dot,
            backgroundColor: config.color,
          }}
        />
      </StatusContainer>
    </Tooltip>
  );
}
