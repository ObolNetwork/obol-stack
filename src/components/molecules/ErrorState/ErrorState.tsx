'use client';

import React from 'react';
import { Box, Text, Button, theme } from '@obolnetwork/obol-ui';
import { LowConnection } from '@/icons';

export interface ErrorStateProps {
  message: string;
  onRetry?: () => void;
  retryLabel?: string;
  className?: string;
}

export const ErrorState: React.FC<ErrorStateProps> = ({
  message,
  onRetry,
  retryLabel = 'Retry',
  className = '',
}) => {
  return (
    <Box
      className={className}
      css={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        gap: '$sm',
        padding: '$lg',
        borderRadius: '$3',
        border: '1px solid $bg05',
      }}
    >
      <LowConnection size="s" color={theme.colors.light.value} />
      <Text
        css={{
          fontSize: '$md',
          color: '$light',
          textAlign: 'center',
        }}
      >
        {message}
      </Text>
      {onRetry && (
        <Button onClick={onRetry} variant="secondary" size="md">
          {retryLabel}
        </Button>
      )}
    </Box>
  );
};
