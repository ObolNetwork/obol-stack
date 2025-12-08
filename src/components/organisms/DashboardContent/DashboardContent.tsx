import { Box, styled } from '@obolnetwork/obol-ui';
import { NetworkStatus } from '../NetworkStatus';
import { NodeOverview } from '../NodeOverview';
import { DashboardData } from '../../../types/dashboard';
import { AgentSidebar } from '../../molecules/AgentSidebar';
import { ErrorState } from '../../molecules/ErrorState';
import { useState } from 'react';

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

export function DashboardContent({
  data,
  error,
  onRetry,
}: DashboardContentProps) {
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  console.log(data);
  return (
    <Container>
      <MainSection as="section" data-sidebar-open={isSidebarOpen}>
        <SectionContainer>
          {error && <ErrorState onRetry={onRetry} />}
          {data &&
            data.apps.map((app, index) => (
              <NetworkStatus
                key={app.appId}
                appData={app}
                defaultExpanded={index === 0}
              />
            ))}
          {data && <NodeOverview data={data.nodeOverview} ports={data.ports} />}
        </SectionContainer>
      </MainSection>

      {data && <AgentSidebar onSidebarToggle={setIsSidebarOpen} data={data} />}
    </Container>
  );
}
