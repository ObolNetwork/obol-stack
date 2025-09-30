import { Box, styled } from '@obolnetwork/obol-ui';
import { NetworkStatus } from '../NetworkStatus';
import { NodeOverview } from '../NodeOverview';
import { DashboardData } from '../../../types/dashboard';

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
});

const SectionContainer = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$lg',
});

export function DashboardContent({ data }: DashboardContentProps) {
  return (
    <Container>
      <MainSection as="section">
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
    </Container>
  );
}
