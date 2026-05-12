import { Box, styled } from '@obolnetwork/obol-ui';
import { NetworksSection } from '../NetworksSection/NetworksSection';
import { NodeOverview } from '../NodeOverview';
import { AgentList } from '../AgentList';
import { AgentRegistrationCard } from '../../molecules/AgentRegistrationCard';
import { ServiceList } from '../ServiceList';
import { AgentRegistry } from '../AgentRegistry';
import { DashboardData } from '../../../types/dashboard';
import { AgentChatSidebar } from '../../molecules/AgentChatSidebar';
import { ErrorState } from '../../molecules/ErrorState';
import { useState, useCallback } from 'react';
import { useAnchorHash } from '../../../hooks/useAnchorHash';
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
  maxWidth: '800px',
  margin: '0 auto',
  width: '100%',

  '@media (min-width: 1500px) and (max-width: 1900px)': {
    transition: 'margin 0.3s ease-in-out',

    '&[data-sidebar-open="true"]': {
      marginLeft: '0px', // smooth animation now works
    },
  },
  '@media (min-width: 1300px) and (max-width: 1500px)': {
    transition: 'margin 0.3s ease-in-out',

    '&[data-sidebar-open="false"]': {
      marginLeft: '10%',
    },

    '&[data-sidebar-open="true"]': {
      marginLeft: '0px', // smooth animation now works
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

export function DashboardContent({
  data,
  error,
  onRetry,
}: DashboardContentProps) {
  const { hasHash } = useAnchorHash({ hash: '#agent' });
  const [isSidebarOpen, setIsSidebarOpen] = useState(hasHash);
  const [chatInstance, setChatInstance] = useState<AgentInstance | null>(null);

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
          <AgentList onInstanceSelect={handleInstanceSelect} />
          <AgentRegistrationCard />
          <ServiceList />
          <AgentRegistry />
          {error && <ErrorState onRetry={onRetry} />}
          {data && (data.localNodes.length > 0 || data.erpcData) && (
            <NetworksSection
              localNodes={data.localNodes}
              erpcData={data.erpcData}
            />
          )}
          {data && <NodeOverview data={data.nodeOverview} ports={data.ports} />}
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
