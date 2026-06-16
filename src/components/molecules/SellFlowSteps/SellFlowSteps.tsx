'use client';

import { Box, Text, styled } from '@obolnetwork/obol-ui';
import { CopyButton } from '@/components/atoms/CopyButton';
import type { SellFlowStep } from '@/lib/sell/flow-steps';

const List = styled('ol', {
  margin: 0,
  padding: 0,
  listStyle: 'none',
  display: 'flex',
  flexDirection: 'column',
  gap: '$sm',
});

const Row = styled('li', {
  display: 'flex',
  gap: '$sm',
  alignItems: 'flex-start',
});

const Index = styled('span', {
  flexShrink: 0,
  width: 22,
  height: 22,
  borderRadius: '$round',
  border: '1px solid $stroke',
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  fontSize: '$1',
  fontFamily: 'monospace',
  color: '$obolGreen',
  marginTop: 2,
});

const Mono = styled('pre', {
  margin: 0,
  padding: '$xs $sm',
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
  fontSize: '$1',
  fontFamily: 'monospace',
  color: '$light',
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-word',
});

const CliRow = styled(Box, {
  display: 'flex',
  alignItems: 'flex-start',
  gap: '$xs',
  marginTop: '$xxs',
});

export interface SellFlowStepsProps {
  steps: SellFlowStep[];
  /** Optional heading above the list. */
  title?: string;
}

/** Numbered sell follow-up steps with optional copyable CLI per step. */
export function SellFlowSteps({ steps, title }: SellFlowStepsProps) {
  if (steps.length === 0) return null;

  return (
    <Box>
      {title ? (
        <Text
          css={{
            fontSize: '$2',
            fontWeight: '$semibold',
            color: '$light',
            marginBottom: '$sm',
          }}
        >
          {title}
        </Text>
      ) : null}
      <List>
        {steps.map((step, i) => (
          <Row key={`${i}-${step.title}`}>
            <Index>{i + 1}</Index>
            <Box css={{ flex: 1, minWidth: 0 }}>
              <Text
                css={{ fontSize: '$2', color: '$light', fontWeight: '$medium' }}
              >
                {step.title}
              </Text>
              {step.detail ? (
                <Text css={{ fontSize: '$1', color: '$body', marginTop: 4 }}>
                  {step.detail}
                </Text>
              ) : null}
              {step.cli ? (
                <CliRow>
                  <Mono>{step.cli}</Mono>
                  <CopyButton text={step.cli} title="Copy command" />
                </CliRow>
              ) : null}
            </Box>
          </Row>
        ))}
      </List>
    </Box>
  );
}
