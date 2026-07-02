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
import { sumChargedRequests24hForAgent } from '@/lib/services/service-usage';
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
  overflowX: 'hidden',
  paddingTop: '$sm',
  paddingRight: '$xs',

  // Mint scrollbar (mirrors the design's .ai-subagent-scroll). Only visible
  // once the list actually scrolls (see the `scroll` variant below).
  scrollbarWidth: 'thin',
  scrollbarColor: 'rgba(47, 228, 171, 0.32) transparent',
  '&::-webkit-scrollbar': { width: 9 },
  '&::-webkit-scrollbar-track': {
    background: 'transparent',
    margin: '4px 0',
  },
  '&::-webkit-scrollbar-thumb': {
    background:
      'linear-gradient(180deg, rgba(47, 228, 171, 0.40), rgba(47, 228, 171, 0.22))',
    borderRadius: 999,
    border: '2px solid transparent',
    backgroundClip: 'padding-box',
  },
  '&::-webkit-scrollbar-thumb:hover': {
    background:
      'linear-gradient(180deg, rgba(47, 228, 171, 0.62), rgba(47, 228, 171, 0.40))',
    backgroundClip: 'padding-box',
  },

  variants: {
    // Cap height + scroll only past 2 sub-agents; 1–2 expand freely.
    scroll: {
      true: { maxHeight: 'min(620px, 60vh)', overflowY: 'auto' },
    },
  },
});

// Tree connector tying sub-agents back to the primary block, per the design:
// a mint trunk down the left with a short branch stub into each card.
const SubAgentTree = styled(Box, {
  position: 'relative',
  paddingLeft: 28,
  display: 'flex',
  flexDirection: 'column',
  gap: '$sm',
});

const Trunk = styled(Box, {
  position: 'absolute',
  left: 9,
  top: 0,
  bottom: 24,
  width: 2,
  background:
    'linear-gradient(180deg, rgba(47,228,171,0.45), rgba(47,228,171,0.12))',
  borderRadius: 2,
});

const BranchItem = styled(Box, {
  position: 'relative',
  '&::before': {
    content: '""',
    position: 'absolute',
    left: -19,
    top: 32,
    width: 19,
    height: 2,
    background: 'rgba(47,228,171,0.35)',
    borderRadius: 2,
  },
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
                    requests24h={sumChargedRequests24hForAgent(
                      offers,
                      instance,
                      { receiveLegacyOrphans: primary.length === 1 }
                    )}
                    onChat={handleInstanceSelect}
                  />
                ))}
              </AgentGrid>
            )}

            {!instancesLoading && !instancesError && subAgents.length > 0 && (
              <AgentsSection>
                <Accordion header={agentsHeader} defaultExpanded={true}>
                  <AgentsScroll scroll={subAgents.length > 2}>
                    <SubAgentTree>
                      <Trunk aria-hidden="true" />
                      {subAgents.map(instance => (
                        <BranchItem key={`${instance.runtime}:${instance.id}`}>
                          <AgentInstanceCard
                            instance={instance}
                            onSelect={handleInstanceSelect}
                            onRefresh={refreshAgentsAndOffers}
                          />
                        </BranchItem>
                      ))}
                    </SubAgentTree>
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
