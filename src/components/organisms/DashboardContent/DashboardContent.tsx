import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { HeroBand } from '../HeroBand';
import { NodeOverview } from '../NodeOverview';
import { NetworksSection } from '../NetworksSection/NetworksSection';
import { ServiceList } from '../ServiceList';
import { AgentRegistrationCard } from '../../molecules/AgentRegistrationCard';
import { AgentRegistry } from '../AgentRegistry';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { SectionTitle } from '../../atoms/SectionTitle';
import { CountBadge } from '../../atoms/CountBadge';
import { EmptyMessage } from '../../atoms/EmptyMessage';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { HomeAgentCard } from '../../home/HomeAgentCard';
import { CreateAgentWorkflow } from '../../home/CreateAgentWorkflow';
import { AgentInstanceCard } from '../../molecules/AgentInstanceCard/AgentInstanceCard';
import { DashboardData } from '../../../types/dashboard';
import { AgentChatSidebar } from '../../molecules/AgentChatSidebar';
import { useState, useCallback } from 'react';
import { useAnchorHash } from '../../../hooks/useAnchorHash';
import { useAgentInstances } from '../../../hooks/useAgents';
import { useServiceOffers } from '../../../hooks/useServiceOffers';
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

const AgentsSection = styled(Box, {
  marginTop: '$md',
  paddingTop: '$md',
  borderTop: '1px solid $stroke',
});

const AgentsScroll = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  maxHeight: 'min(360px, 50vh)',
  overflowY: 'auto',
  paddingTop: '$sm',
  paddingRight: '$xs',
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
  const {
    offers,
    pendingOffers,
    loading: offersLoading,
    error: offersError,
    refetch: refetchOffers,
  } = useServiceOffers();
  const refreshAgentsAndOffers = useCallback(() => {
    refetch();
    refetchOffers();
  }, [refetch, refetchOffers]);
  const { primary, subAgents } = partitionInstances(instances);

  const uptimeHours = data?.nodeOverview?.uptime?.hours ?? 0;

  const handleInstanceSelect = (instance: AgentInstance) => {
    setIsSidebarOpen(true);
    setChatInstance(instance);
  };

  const handleChatClose = () => {
    setChatInstance(null);
  };

  const agentsHeader = (
    <Flex css={{ alignItems: 'center', gap: '$sm', paddingRight: '$xl' }}>
      <SectionTitle level={2}>Sub-agents</SectionTitle>
      {subAgents.length > 0 && <CountBadge count={subAgents.length} />}
      <Text css={{ fontSize: '$2', color: '$muted' }}>Agents you can sell</Text>
    </Flex>
  );

  return (
    <Container>
      <MainSection as="section" data-sidebar-open={isSidebarOpen}>
        <SectionContainer>
          <HeroBand data={data} />

          {error && <ErrorState onRetry={onRetry} />}

          {data && <NodeOverview data={data.nodeOverview} ports={data.ports} />}

          <Card css={{ padding: '$lg' }}>
            <Flex
              css={{ alignItems: 'center', gap: '$sm', marginBottom: '$md' }}
            >
              <SectionTitle>Agents</SectionTitle>
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
                the default stack agent, or{' '}
                <code>obol agent new &lt;name&gt;</code> for an additional
                agent.
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
              <AgentsSection>
                <Accordion header={agentsHeader} defaultExpanded={true}>
                  <AgentsScroll>
                    {subAgents.map(instance => (
                      <AgentInstanceCard
                        key={`${instance.runtime}:${instance.id}`}
                        instance={instance}
                        onSelect={handleInstanceSelect}
                        onRefresh={refreshAgentsAndOffers}
                      />
                    ))}
                  </AgentsScroll>
                </Accordion>
              </AgentsSection>
            )}

            {!instancesLoading && !instancesError && (
              <AgentsSection>
                <CreateAgentWorkflow />
              </AgentsSection>
            )}
          </Card>

          <ServiceList
            offers={offers}
            pendingOffers={pendingOffers}
            loading={offersLoading}
            error={offersError}
            onRefresh={refreshAgentsAndOffers}
          />

          <AgentRegistrationCard />

          <AgentRegistry />

          {data && (data.localNodes.length > 0 || data.erpcData) && (
            <NetworksSection
              localNodes={data.localNodes}
              erpcData={data.erpcData}
            />
          )}
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
