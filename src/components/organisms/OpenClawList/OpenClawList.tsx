'use client';

import { Box, Flex, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { CountBadge } from '../../atoms/CountBadge';
import { SectionTitle } from '../../atoms/SectionTitle';
import { EmptyMessage } from '../../atoms/EmptyMessage';
import { OpenClawInstanceCard } from '../../molecules/OpenClawInstanceCard';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { useOpenClawInstances } from '../../../hooks/useOpenClaw';
import type { OpenClawInstance } from '@/types/openclaw';

interface OpenClawListProps {
  onInstanceSelect?: (instance: OpenClawInstance) => void;
}

const InstancesList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  paddingTop: '$lg',
});

export function OpenClawList({ onInstanceSelect }: OpenClawListProps) {
  const { instances, loading, error } = useOpenClawInstances();

  const headerContent = (
    <Flex css={{ alignItems: 'center', gap: '$sm', paddingRight: '$xl' }}>
      <SectionTitle>AI Agent Instances</SectionTitle>
      {!loading && instances.length > 0 && (
        <CountBadge count={instances.length} />
      )}
    </Flex>
  );

  return (
    <Card section>
      <Accordion header={headerContent} defaultExpanded={true}>
        <InstancesList>
          {loading && (
            <LoadingState size="small" message="Loading instances..." />
          )}
          {error && <ErrorState message="Failed to load instances" />}
          {!loading && !error && instances.length === 0 && (
            <EmptyMessage>
              No OpenClaw instances found. Run the command '
              <code>obol agent init</code>' to create one.
            </EmptyMessage>
          )}
          {instances.map(instance => (
            <OpenClawInstanceCard
              key={instance.id}
              instance={instance}
              onSelect={onInstanceSelect}
            />
          ))}
        </InstancesList>
      </Accordion>
    </Card>
  );
}
