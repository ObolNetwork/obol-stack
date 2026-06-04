'use client';

import { Box, Flex, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { CountBadge } from '../../atoms/CountBadge';
import { SectionTitle } from '../../atoms/SectionTitle';
import { EmptyMessage } from '../../atoms/EmptyMessage';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { AgentInstanceCard } from '../../molecules/AgentInstanceCard/AgentInstanceCard';
import { useAgentInstances } from '../../../hooks/useAgents';
import type { AgentInstance } from '@/types/agent';

interface AgentListProps {
  onInstanceSelect?: (instance: AgentInstance) => void;
}

const InstancesList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  paddingTop: '$lg',
});

export function AgentList({ onInstanceSelect }: AgentListProps) {
  const { instances, loading, error, refetch } = useAgentInstances();

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
          {error && (
            <ErrorState
              message="Failed to load agent instances"
              onRetry={refetch}
            />
          )}
          {!loading && !error && instances.length === 0 && (
            <EmptyMessage>
              No agent instances found. Run <code>obol agent init</code> for the
              default Hermes agent, or <code>obol agent new &lt;name&gt;</code>{' '}
              with <code>--runtime hermes</code> /{' '}
              <code>--runtime openclaw</code> for a named runtime agent.
            </EmptyMessage>
          )}
          {instances.map(instance => (
            <AgentInstanceCard
              key={`${instance.runtime}:${instance.id}`}
              instance={instance}
              onSelect={onInstanceSelect}
            />
          ))}
        </InstancesList>
      </Accordion>
    </Card>
  );
}
