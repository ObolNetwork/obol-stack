import { Box, styled } from '@obolnetwork/obol-ui';
import { NetworkStatus } from '../NetworkStatus';
import { NodeOverview } from '../NodeOverview';
import { DashboardData } from '../../../types/dashboard';
import { AgentSidebar } from '../../molecules/AgentSidebar';
import { ErrorState } from '../../molecules/ErrorState';
import { useState } from 'react';
import { useAnchorHash } from '../../../hooks/useAnchorHash';

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

    '&[data-sidebar-open="false"]': {
      marginLeft: '25%',
    },

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
          {data && (
            <NodeOverview data={data.nodeOverview} ports={data.ports} />
          )}
        </SectionContainer>
      </MainSection>

      {data && (
        <AgentSidebar
          onSidebarToggle={setIsSidebarOpen}
          data={data}
          defaultOpen={hasHash}
        />
      )}
    </Container>
  );
}
