'use client';

import React from 'react';
import { Box, Text, Button } from '@obolnetwork/obol-ui';

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
        gap: '16px',
        padding: '24px',
        borderRadius: '8px',
        border: '1px solid #2D4D53',
        background: '#091011',
        color: '#E1E9EB',
      }}
    >
      {/* <Icon name="low-connection" color='#E1E9EB' /> */}
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
