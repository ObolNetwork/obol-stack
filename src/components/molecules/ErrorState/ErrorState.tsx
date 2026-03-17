'use client';

import React from 'react';
import { Box, Text, Button, theme } from '@obolnetwork/obol-ui';
import { LowConnection } from '@/icons';

export interface ErrorStateProps {
  message?: string;
  onRetry?: () => void;
}

export const ErrorState: React.FC<ErrorStateProps> = ({
  message = 'Failed to fetch',
  onRetry,
}) => {
  return (
    <Box
      css={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        padding: '$lg',
        borderRadius: '$3',
        border: '1px solid $bg05',
      }}
    >
      <LowConnection size="s" color={theme.colors.light.value} />
      <Text
        css={{
          fontSize: '$3',
          color: '$light',
          textAlign: 'center',
          fontWeight: '$semibold',
          lineHeight: '$tall2',
          marginTop: '$xxxs',
          marginBottom: '$xxxs',
        }}
      >
        API connection failed
      </Text>
      <Text
        css={{
          fontSize: '$3',
          color: '$body',
          textAlign: 'center',
          fontWeight: '$medium',
          lineHeight: '$shorter',
          marginBottom: '$sm',
        }}
      >
        {message}
      </Text>
      {onRetry && (
        <Button onClick={onRetry} color="secondary" size="md">
          Retry
        </Button>
      )}
    </Box>
  );
};
