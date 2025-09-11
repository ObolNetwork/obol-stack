'use client';

import React from 'react';
import { Box, Text, Button } from '@obolnetwork/obol-ui';

export interface ErrorStateProps {
  title?: string;
  message: string;
  onRetry?: () => void;
  retryLabel?: string;
  variant?: 'banner' | 'centered';
  className?: string;
}

export const ErrorState: React.FC<ErrorStateProps> = ({
  title = 'Error',
  message,
  onRetry,
  retryLabel = 'Retry',
  variant = 'banner',
  className = '',
}) => {
  if (variant === 'centered') {
    return (
      <Box
        className={className}
        css={{
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
          height: '50vh',
          gap: '$md',
        }}
      >
        <Text
          css={{ fontSize: '$lg', color: '$red11', fontWeight: '$semibold' }}
        >
          {title}
        </Text>
        <Text
          css={{
            fontSize: '$md',
            color: '$gray11',
            textAlign: 'center',
            maxWidth: '400px',
          }}
        >
          {message}
        </Text>
        {onRetry && (
          <Button onClick={onRetry} variant="outline" size="md">
            {retryLabel}
          </Button>
        )}
      </Box>
    );
  }

  // Banner variant (default)
  return (
    <Box
      className={className}
      css={{
        backgroundColor: '$amber3',
        border: '1px solid $amber6',
        borderRadius: '$md',
        padding: '$sm',
        marginBottom: '$md',
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
      }}
    >
      <Text css={{ color: '$amber11', fontSize: '$sm' }}>{message}</Text>
      {onRetry && (
        <Button onClick={onRetry} variant="outline" size="sm">
          {retryLabel}
        </Button>
      )}
    </Box>
  );
};
