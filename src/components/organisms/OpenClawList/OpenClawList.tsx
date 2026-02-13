'use client';

import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { OpenClawInstanceCard } from '../../molecules/OpenClawInstanceCard';
import { useOpenClawInstances } from '../../../hooks/useOpenClaw';
import type { OpenClawInstance } from '@/types/openclaw';

interface OpenClawListProps {
  onInstanceSelect?: (instance: OpenClawInstance) => void;
}

const Container = styled(Box, {
  borderRadius: '$4',
  backgroundColor: '$bg02',
  border: '1px solid $stroke',
  overflow: 'hidden',
});

const Header = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  padding: '$md $lg',
  borderBottom: '1px solid $stroke',
});

const TitleRow = styled(Flex, {
  alignItems: 'center',
  gap: '$sm',
});

const Title = styled(Text, {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
});

const Badge = styled(Box, {
  backgroundColor: '#2FE4AB20',
  color: '#2FE4AB',
  borderRadius: '12px',
  padding: '2px 10px',
  fontSize: '$1',
  fontWeight: '$semibold',
});

const InstancesContainer = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  padding: '$md $lg',
});

const EmptyMessage = styled(Text, {
  color: '$body',
  fontSize: '$3',
  textAlign: 'center',
  padding: '$lg 0',
});

export function OpenClawList({ onInstanceSelect }: OpenClawListProps) {
  const { instances, loading, error } = useOpenClawInstances();

  return (
    <Container>
      <Header>
        <TitleRow>
          <Title> AI Agent Instances</Title>
          {!loading && instances.length > 0 && (
            <Badge>{instances.length}</Badge>
          )}
        </TitleRow>
      </Header>

      <InstancesContainer>
        {loading && <EmptyMessage>Loading instances...</EmptyMessage>}
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
      </InstancesContainer>
    </Container>
  );
}
