import { Box, styled } from '@obolnetwork/obol-ui';
import { NetworkStatus } from '../NetworkStatus';
import { NodeOverview } from '../NodeOverview';
import { DashboardData } from '../../../types/dashboard';
import { AgentSidebar } from '../../molecules/AgentSidebar';
import { useState } from 'react';

interface DashboardContentProps {
  data: DashboardData;
}

const Container = styled(Box, {
  marginTop: '$lg',
});

const MainSection = styled(Box, {
  maxWidth: '800px',
  margin: '0 auto',
  width: '100%',
  transition: 'margin-left 0.3s ease-in-out',

  // Adjust layout when sidebar is present
  // The CopilotSidebar typically renders with a fixed position
  // We'll use a CSS approach that detects the sidebar presence
  '&[data-sidebar-open="true"]': {
    marginLeft: '0',
    marginRight: 'auto',
  },
});

const SectionContainer = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$lg',
});

export function DashboardContent({ data }: DashboardContentProps) {
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);

  return (
    <Container>
      <MainSection as="section" data-sidebar-open={isSidebarOpen}>
        <SectionContainer>
          {data.chains.map((chain, index) => (
            <NetworkStatus
              key={chain.chainName}
              chainData={chain}
              defaultExpanded={index === 0}
            />
          ))}
          <NodeOverview data={data.nodeOverview} ports={data.ports} />
        </SectionContainer>
      </MainSection>

      <AgentSidebar onSidebarToggle={setIsSidebarOpen} data={data} />
    </Container>
  );
}
