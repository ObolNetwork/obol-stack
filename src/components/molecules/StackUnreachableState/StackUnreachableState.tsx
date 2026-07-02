'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { ExclamationTriangleIcon } from '@radix-ui/react-icons';
import { Button } from '@/components/atoms/Button';

export interface StackUnreachableStateProps {
  /** Optional detail line (e.g. raw error message) shown below the help. */
  detail?: string;
  /** Optional retry callback — when omitted the retry button is hidden. */
  onRetry?: () => void;
  /** Override the help text. Default points at `obol stack up`. */
  helpText?: string;
}

const Card = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'flex-start',
  gap: '$xs',
  padding: '$lg',
  borderRadius: '$4',
  border: '1px solid $stroke',
  background: '$bg02',
  color: '$light',
});

const TitleRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
});

const Title = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
});

const Help = styled(Text, {
  fontSize: '$2',
  color: '$body',
  lineHeight: 1.5,
});

const Mono = styled('code', {
  fontFamily: 'monospace',
  fontSize: '$1',
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$1',
  padding: '0 $xxs',
  color: '$light',
});

const Detail = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontFamily: 'monospace',
  marginTop: '$xs',
  wordBreak: 'break-word',
});

/**
 * Single shared "the cluster you're talking to isn't answering" surface.
 * Used by the marketplace pages and the dashboard so the user sees one
 * consistent message regardless of which route surfaced the failure.
 */
export function StackUnreachableState({
  detail,
  onRetry,
  helpText,
}: StackUnreachableStateProps) {
  return (
    <Card role="alert" aria-live="polite">
      <TitleRow>
        <ExclamationTriangleIcon width={20} height={20} color="#F4B400" />
        <Title>Obol Stack unreachable</Title>
      </TitleRow>
      {helpText ? (
        <Help>{helpText}</Help>
      ) : (
        <Help>
          The frontend can&apos;t reach your local stack. Start it with{' '}
          <Mono>obol stack up</Mono>, or check that your <Mono>kubeconfig</Mono>{' '}
          points at a running cluster.
        </Help>
      )}
      {detail ? <Detail>{detail}</Detail> : null}
      {onRetry ? (
        <Button
          type="button"
          variant="secondary"
          size="md"
          css={{ marginTop: '$xs' }}
          onClick={onRetry}
        >
          Retry
        </Button>
      ) : null}
    </Card>
  );
}
