import { Box, Flex, styled } from '@obolnetwork/obol-ui';
import { HeroBand } from '../HeroBand';
import { NodeOverview } from '../NodeOverview';
import { Card } from '../../atoms/Card';
import { SectionTitle } from '../../atoms/SectionTitle';
import { CountBadge } from '../../atoms/CountBadge';
import { HomeAgentCard } from '../../home/HomeAgentCard';
import { HomeListingsWidget } from '../../home/HomeListingsWidget';
import { HomeNetworksWidget } from '../../home/HomeNetworksWidget';
import { HomeRegistryBanner } from '../../home/HomeRegistryBanner';
import { DashboardData } from '../../../types/dashboard';
import { AgentChatSidebar } from '../../molecules/AgentChatSidebar';
import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { useAnchorHash } from '../../../hooks/useAnchorHash';
import { useAgentInstances } from '../../../hooks/useAgents';
import { useTopNav } from '../../../hooks/useTopNav';
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

    '&[data-sidebar-open="true"]': {
      marginLeft: '0px',
    },
  },
  '@media (min-width: 1300px) and (max-width: 1500px)': {
    transition: 'margin 0.3s ease-in-out',

    '&[data-sidebar-open="false"]': {
      marginLeft: '10%',
    },

    '&[data-sidebar-open="true"]': {
      marginLeft: '0px',
    },
  },
  '@media (max-width: 1300px)': {
    marginLeft: '0px',
  },
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
  '@media (max-width: 768px)': {
    gridTemplateColumns: '1fr',
  },
});

export function DashboardContent({ data }: DashboardContentProps) {
  const { hasHash } = useAnchorHash({ hash: '#agent' });
  const [isSidebarOpen, setIsSidebarOpen] = useState(hasHash);
  const [chatInstance, setChatInstance] = useState<AgentInstance | null>(null);
  const { instances } = useAgentInstances();
  const { onNavSelect } = useTopNav();
  const router = useRouter();

  const uptimeHours = data?.nodeOverview?.uptime?.hours ?? 0;

  const handleInstanceSelect = (instance: AgentInstance) => {
    setIsSidebarOpen(true);
    setChatInstance(instance);
  };

  const handleChatClose = () => {
    setChatInstance(null);
  };

  return (
    <Container>
      <MainSection as="section" data-sidebar-open={isSidebarOpen}>
        <SectionContainer>
          {/* Hero band */}
          <HeroBand data={data} />

          {/* Node Overview */}
          {data && <NodeOverview data={data.nodeOverview} ports={data.ports} />}

          {/* AI Agent Instances — 2-column grid */}
          <Card css={{ padding: '$lg' }}>
            <Flex
              css={{ alignItems: 'center', gap: '$sm', marginBottom: '$md' }}
            >
              <SectionTitle>AI Agent Instances</SectionTitle>
              {instances.length > 0 && <CountBadge count={instances.length} />}
            </Flex>
            <AgentGrid>
              {instances.map(instance => (
                <HomeAgentCard
                  key={`${instance.runtime}:${instance.id}`}
                  instance={instance}
                  uptimeHours={uptimeHours}
                  onChat={handleInstanceSelect}
                />
              ))}
            </AgentGrid>
          </Card>

          {/* My Listings */}
          <HomeListingsWidget
            onManage={() => router.push('/marketplace/listings')}
          />

          {/* Networks */}
          {data?.erpcData && <HomeNetworksWidget erpcData={data.erpcData} />}

          {/* Agent Registry banner */}
          <HomeRegistryBanner
            onBrowseMarketplace={() => onNavSelect('marketplace')}
          />
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
