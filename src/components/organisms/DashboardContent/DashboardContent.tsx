import { Box, styled } from '@obolnetwork/obol-ui';
import { NetworkStatus } from '../NetworkStatus';
import { NodeOverview } from '../NodeOverview';
import { DashboardData } from '../../../types/dashboard';

interface DashboardContentProps {
  data: DashboardData;
}

const GridContainer = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '1fr',
  gap: '$lg',
  marginTop: '$lg',

  '@bp2': {
    gridTemplateColumns: 'repeat(12, 1fr)',
  },
});

const MainSection = styled(Box, {
  '@bp2': {
    gridColumn: 'span 8',
  },
});

const Sidebar = styled(Box, {
  '@bp2': {
    gridColumn: 'span 4',
  },
});

const SectionContainer = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$lg',
});

export function DashboardContent({ data }: DashboardContentProps) {
  return (
    <GridContainer>
      <MainSection as="section">
        <SectionContainer>
          <NetworkStatus data={data.ethereum} clients={data.clients} />
        </SectionContainer>
      </MainSection>

      <Sidebar as="aside">
        <SectionContainer>
          <NodeOverview data={data.nodeOverview} ports={data.ports} />
        </SectionContainer>
      </Sidebar>
    </GridContainer>
  );
}
