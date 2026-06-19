import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { HeroBand } from '../HeroBand';
import { NodeOverview } from '../NodeOverview';
import { AgentRegistrationCard } from '../../molecules/AgentRegistrationCard';
import { AgentRegistry } from '../AgentRegistry';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { SectionTitle } from '../../atoms/SectionTitle';
import { CountBadge } from '../../atoms/CountBadge';
import { EmptyMessage } from '../../atoms/EmptyMessage';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { CopyButton } from '../../atoms/CopyButton';
import { HomeAgentCard } from '../../home/HomeAgentCard';
import { HomeListingsWidget } from '../../home/HomeListingsWidget';
import { HomeNetworksWidget } from '../../home/HomeNetworksWidget';
import { AgentInstanceCard } from '../../molecules/AgentInstanceCard/AgentInstanceCard';
import { DashboardData } from '../../../types/dashboard';
import { AgentChatSidebar } from '../../molecules/AgentChatSidebar';
import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { useAnchorHash } from '../../../hooks/useAnchorHash';
import { useAgentInstances } from '../../../hooks/useAgents';
import { buildAgentNewCli } from '@/lib/sell/cli-commands';
import type { AgentInstance } from '@/types/agent';

interface DashboardContentProps {
  data: DashboardData | null;
  error?: string | Error | null;
  onRetry?: () => void;
}

const Container = styled(Box, {
  marginTop: '$lg',
});

const MainSection = styled(Box, {
  maxWidth: '1160px',
  margin: '0 auto',
  width: '100%',

  '@media (min-width: 1500px) and (max-width: 1900px)': {
    transition: 'margin 0.3s ease-in-out',
    '&[data-sidebar-open="true"]': { marginLeft: '0px' },
  },
  '@media (min-width: 1300px) and (max-width: 1500px)': {
    transition: 'margin 0.3s ease-in-out',
    '&[data-sidebar-open="false"]': { marginLeft: '10%' },
    '&[data-sidebar-open="true"]': { marginLeft: '0px' },
  },
  '@media (max-width: 1300px)': { marginLeft: '0px' },
});

const SectionContainer = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$lg',
});

const AgentGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'repeat(2, 1fr)',
  gap: '$sm',
  '@media (max-width: 768px)': { gridTemplateColumns: '1fr' },
});

const SubAgentsSection = styled(Box, {
  marginTop: '$md',
  paddingTop: '$md',
  borderTop: '1px solid rgba(255, 255, 255, 0.08)',
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

const CreateAgentRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  padding: '$xs',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
  marginTop: '$xs',
});

const CREATE_AGENT_EXAMPLE = buildAgentNewCli({
  name: 'my-agent',
  model: 'claude-sonnet-4-6',
  skills: 'addresses,gas',
});

function partitionInstances(instances: AgentInstance[]) {
  return {
    primary: instances.filter(i => !i.isSubAgent),
    subAgents: instances.filter(i => i.isSubAgent),
  };
}

export function DashboardContent({
  data,
  error,
  onRetry,
}: DashboardContentProps) {
  const { hasHash } = useAnchorHash({ hash: '#agent' });
  const [isSidebarOpen, setIsSidebarOpen] = useState(hasHash);
  const [chatInstance, setChatInstance] = useState<AgentInstance | null>(null);
  const {
    instances,
    loading: instancesLoading,
    error: instancesError,
    refetch,
  } = useAgentInstances();
  const { primary, subAgents } = partitionInstances(instances);
  const router = useRouter();

  const uptimeHours = data?.nodeOverview?.uptime?.hours ?? 0;

  const handleInstanceSelect = (instance: AgentInstance) => {
    setIsSidebarOpen(true);
    setChatInstance(instance);
  };

  const handleChatClose = () => {
    setChatInstance(null);
  };

  const subAgentsHeader = (
    <Flex css={{ alignItems: 'center', gap: '$sm', paddingRight: '$xl' }}>
      <Text css={{ fontSize: '$3', fontWeight: '$semibold', color: '$light' }}>
        Sub-agents
      </Text>
      {subAgents.length > 0 && <CountBadge count={subAgents.length} />}
    </Flex>
  );

  return (
    <Container>
      <MainSection as="section" data-sidebar-open={isSidebarOpen}>
        <SectionContainer>
          <HeroBand data={data} />

          {error && <ErrorState onRetry={onRetry} />}

          {data && <NodeOverview data={data.nodeOverview} ports={data.ports} />}

          {/* AI Agent Instances — her grid design + sub-agents section */}
          <Card css={{ padding: '$lg' }}>
            <Flex
              css={{ alignItems: 'center', gap: '$sm', marginBottom: '$md' }}
            >
              <SectionTitle>AI Agent Instances</SectionTitle>
              {!instancesLoading && instances.length > 0 && (
                <CountBadge count={instances.length} />
              )}
            </Flex>

            {instancesLoading && (
              <LoadingState size="small" message="Loading instances..." />
            )}
            {instancesError && (
              <ErrorState
                message="Failed to load agent instances"
                onRetry={refetch}
              />
            )}
            {!instancesLoading && !instancesError && instances.length === 0 && (
              <EmptyMessage>
                No agent instances found. Run <code>obol agent init</code> for
                the default Hermes agent, or{' '}
                <code>obol agent new &lt;name&gt;</code> for a sub-agent.
              </EmptyMessage>
            )}

            {primary.length > 0 && (
              <AgentGrid
                css={
                  primary.length === 1
                    ? { gridTemplateColumns: '1fr' }
                    : undefined
                }
              >
                {primary.map(instance => (
                  <HomeAgentCard
                    key={`${instance.runtime}:${instance.id}`}
                    instance={instance}
                    uptimeHours={uptimeHours}
                    onChat={handleInstanceSelect}
                  />
                ))}
              </AgentGrid>
            )}

            {!instancesLoading && !instancesError && subAgents.length > 0 && (
              <SubAgentsSection>
                <Accordion header={subAgentsHeader} defaultExpanded={false}>
                  <SubAgentsScroll>
                    {subAgents.map(instance => (
                      <AgentInstanceCard
                        key={`${instance.runtime}:${instance.id}`}
                        instance={instance}
                        onSelect={handleInstanceSelect}
                      />
                    ))}
                  </SubAgentsScroll>
                </Accordion>
              </SubAgentsSection>
            )}

            {!instancesLoading && !instancesError && (
              <SubAgentsSection>
                <Text
                  css={{
                    fontSize: '$3',
                    fontWeight: '$semibold',
                    color: '$light',
                  }}
                >
                  Create a sub-agent
                </Text>
                <CreateAgentRow>
                  <code
                    style={{
                      flex: 1,
                      fontSize: '12px',
                      color: 'var(--colors-light)',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                    title={CREATE_AGENT_EXAMPLE}
                  >
                    {CREATE_AGENT_EXAMPLE}
                  </code>
                  <CopyButton
                    text={CREATE_AGENT_EXAMPLE}
                    title="Copy obol agent new"
                  />
                </CreateAgentRow>
              </SubAgentsSection>
            )}
          </Card>

          <AgentRegistrationCard />

          <HomeListingsWidget
            onManage={() => router.push('/marketplace/listings')}
          />

          <AgentRegistry />

          {data?.erpcData && <HomeNetworksWidget erpcData={data.erpcData} />}
        </SectionContainer>
      </MainSection>

      <AgentChatSidebar
        isSidebarOpen={isSidebarOpen}
        instance={chatInstance}
        onClose={handleChatClose}
        onSidebarToggle={setIsSidebarOpen}
      />
    </Container>
  );
}
