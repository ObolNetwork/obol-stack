'use client';

import type { ReactNode } from 'react';
import NextLink from 'next/link';
import { Box, Text, styled } from '@obolnetwork/obol-ui';

import { Button } from '@/components/atoms/Button';
import { focusVisible } from '@/lib/ui/theme';

const Panel = styled(Box, {
  maxWidth: 560,
  width: '100%',
  margin: '$3xl auto',
  padding: '$2xl $xl',
  borderRadius: 12,
  border: '1px solid $stroke',
  backgroundColor: '$bg02',
  textAlign: 'center',
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  gap: '$sm',

  '@sm': {
    margin: '$xl auto',
    padding: '$xl $md',
  },
});

const Code = styled('span', {
  fontFamily: 'monospace',
  fontSize: '$2',
  letterSpacing: '0.12em',
  textTransform: 'uppercase',
  color: '$obolGreen',
});

const Actions = styled(Box, {
  display: 'flex',
  flexWrap: 'wrap',
  justifyContent: 'center',
  gap: '$sm',
  marginTop: '$sm',
});

const LinkButton = styled(NextLink, {
  display: 'inline-flex',
  alignItems: 'center',
  height: 40,
  padding: '0 $md',
  borderRadius: '$pill',
  border: '1px solid $stroke',
  backgroundColor: '$bg03',
  color: '$light',
  fontSize: '$3',
  fontWeight: '$semibold',
  transition: 'border-color 120ms, background 120ms',
  '&:hover': { borderColor: '$obolGreen', backgroundColor: '$bg04' },
  ...focusVisible,
});

export interface StatusPanelProps {
  /** Short code shown above the title, e.g. "404". */
  code: string;
  title: string;
  description: ReactNode;
  /** Primary link destination; defaults to the dashboard. */
  homeHref?: string;
  homeLabel?: string;
  /** Optional retry action (error boundaries). */
  onRetry?: () => void;
}

/** Centered dashboard-style card for 404 / error / empty route states. */
export function StatusPanel({
  code,
  title,
  description,
  homeHref = '/',
  homeLabel = 'Back to dashboard',
  onRetry,
}: StatusPanelProps) {
  return (
    <Panel role="alert" aria-labelledby="status-panel-title">
      <Code>{code}</Code>
      <Text
        as="h1"
        id="status-panel-title"
        css={{
          fontSize: '$6',
          fontWeight: '$bold',
          color: '$light',
          margin: 0,
        }}
      >
        {title}
      </Text>
      <Text css={{ fontSize: '$3', color: '$body', lineHeight: 1.5 }}>
        {description}
      </Text>
      <Actions>
        {onRetry && (
          <Button
            type="button"
            variant="primary"
            size="md"
            pill
            onClick={onRetry}
          >
            Try again
          </Button>
        )}
        <LinkButton href={homeHref}>{homeLabel}</LinkButton>
      </Actions>
    </Panel>
  );
}
