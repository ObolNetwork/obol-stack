'use client';

import React from 'react';
import { Box, Text } from '@obolnetwork/obol-ui';

export interface LoadingStateProps {
  message?: string;
  className?: string;
}

export const LoadingState: React.FC<LoadingStateProps> = ({
  message = 'Loading...',
  className = '',
}) => {
  return (
    <Box
      className={className}
      css={{
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'center',
        height: '50vh',
      }}
    >
      <Text css={{ fontSize: '$lg', color: '$gray11' }}>{message}</Text>
    </Box>
  );
};
