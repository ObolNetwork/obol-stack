'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { CountBadge } from '../../atoms/CountBadge';
import { OpenClawInstanceCard } from '../../molecules/OpenClawInstanceCard';
import { LoadingState } from '../../molecules/LoadingState';
import { useOpenClawInstances } from '../../../hooks/useOpenClaw';
import type { OpenClawInstance } from '@/types/openclaw';

interface OpenClawListProps {
  onInstanceSelect?: (instance: OpenClawInstance) => void;
}

const SectionTitle = styled(Text, {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
});

const InstancesList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  paddingTop: '$lg',
});

const EmptyMessage = styled(Text, {
  color: '$body',
  fontSize: '$3',
  textAlign: 'center',
  padding: '$lg 0',
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
    <Card css={{ position: 'relative', overflow: 'hidden', padding: '0' }}>
      <Accordion header={headerContent} defaultExpanded={true}>
        <InstancesList>
          {loading && (
            <LoadingState size="small" message="Loading instances..." />
          )}
          {error && <EmptyMessage>Failed to load instances</EmptyMessage>}
          {!loading && !error && instances.length === 0 && (
            <EmptyMessage>No OpenClaw instances found</EmptyMessage>
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
