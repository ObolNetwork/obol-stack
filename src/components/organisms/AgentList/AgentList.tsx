'use client';

import { Box, Flex, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { CountBadge } from '../../atoms/CountBadge';
import { SectionTitle } from '../../atoms/SectionTitle';
import { EmptyMessage } from '../../atoms/EmptyMessage';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { CopyButton } from '../../atoms/CopyButton';
import { AgentInstanceCard } from '../../molecules/AgentInstanceCard/AgentInstanceCard';
import { useAgentInstances } from '../../../hooks/useAgents';
import { buildAgentNewCli } from '@/lib/sell/cli-commands';
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

const SubAgentsScroll = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  maxHeight: 'min(360px, 50vh)',
  overflowY: 'auto',
  paddingTop: '$sm',
  paddingRight: '$xs',
});

const SubAgentsSection = styled(Box, {
  marginTop: '$md',
  paddingTop: '$md',
  borderTop: '1px solid rgba(255, 255, 255, 0.08)',
});

const SubAgentsTitle = styled('span', {
  fontSize: '$3',
  fontWeight: '$semibold',
  color: '$light',
});

const CreateAgentRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  padding: '$xs',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
  marginTop: '$xs',
});

const CreateAgentCmd = styled('code', {
  flex: 1,
  fontSize: '$1',
  color: '$light',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
});

const CREATE_AGENT_EXAMPLE = buildAgentNewCli({
  name: 'my-agent',
  model: 'claude-sonnet-4-6',
  skills: 'addresses,gas',
});

function partitionInstances(instances: AgentInstance[]) {
  const primary: AgentInstance[] = [];
  const subAgents: AgentInstance[] = [];

  for (const instance of instances) {
    if (instance.isSubAgent) {
      subAgents.push(instance);
    } else {
      primary.push(instance);
    }
  }

  return { primary, subAgents };
}

export function AgentList({ onInstanceSelect }: AgentListProps) {
  const { instances, loading, error, refetch } = useAgentInstances();
  const { primary, subAgents } = partitionInstances(instances);

  const headerContent = (
    <Flex css={{ alignItems: 'center', gap: '$sm', paddingRight: '$xl' }}>
      <SectionTitle>AI Agent Instances</SectionTitle>
      {!loading && instances.length > 0 && (
        <CountBadge count={instances.length} />
      )}
    </Flex>
  );

  const subAgentsHeader = (
    <Flex css={{ alignItems: 'center', gap: '$sm', paddingRight: '$xl' }}>
      <SubAgentsTitle>Sub-agents</SubAgentsTitle>
      {!loading && subAgents.length > 0 && (
        <CountBadge count={subAgents.length} />
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
              for a sub-agent. Use <strong>Sell</strong> on a card to publish a
              paid listing (shown under Obol Services).
            </EmptyMessage>
          )}
          {primary.map(instance => (
            <AgentInstanceCard
              key={`${instance.runtime}:${instance.id}`}
              instance={instance}
              onSelect={onInstanceSelect}
            />
          ))}

          {!loading && !error && subAgents.length > 0 && (
            <SubAgentsSection>
              <Accordion header={subAgentsHeader} defaultExpanded={true}>
                <SubAgentsScroll>
                  {subAgents.map(instance => (
                    <AgentInstanceCard
                      key={`${instance.runtime}:${instance.id}`}
                      instance={instance}
                      onSelect={onInstanceSelect}
                    />
                  ))}
                </SubAgentsScroll>
              </Accordion>
            </SubAgentsSection>
          )}

          {!loading && !error && (
            <SubAgentsSection>
              <SubAgentsTitle>Create a sub-agent</SubAgentsTitle>
              <CreateAgentRow>
                <CreateAgentCmd title={CREATE_AGENT_EXAMPLE}>
                  {CREATE_AGENT_EXAMPLE}
                </CreateAgentCmd>
                <CopyButton
                  text={CREATE_AGENT_EXAMPLE}
                  title="Copy obol agent new"
                />
              </CreateAgentRow>
            </SubAgentsSection>
          )}
        </InstancesList>
      </Accordion>
    </Card>
  );
}
