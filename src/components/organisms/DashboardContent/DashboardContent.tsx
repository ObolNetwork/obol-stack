import { Box, styled } from '@obolnetwork/obol-ui';
import { NetworkStatus } from '../NetworkStatus';
import { NodeOverview } from '../NodeOverview';
import { ChainIcon } from '../../atoms/ChainIcon';
import { DashboardData } from '../../../types/dashboard';

interface DashboardContentProps {
  data: DashboardData;
}

const GridContainer = styled(Box, {
  // display: 'grid',
  // gridTemplateColumns: '1fr',
  // gap: '$lg',
  marginTop: '$lg',

  // '@bp2': {
  //   gridTemplateColumns: 'repeat(12, 1fr)',
  // },
});

const MainSection = styled(Box, {
  maxWidth: '1200px',
  margin: '0 auto',
  width: '100%',
  // '@bp2': {
  //   gridColumn: 'span 8',
  // },
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
          <NetworkStatus
            data={data.ethereum}
            clients={data.clients}
            networkName="Ethereum"
            networkIcon={<ChainIcon chain="ethereum" size={24} />}
          />
          <NetworkStatus
            data={data.aztec}
            networkName="Aztec"
            networkIcon={<ChainIcon chain="aztec" size={24} />}
          />
          <NodeOverview data={data.nodeOverview} ports={data.ports} />
        </SectionContainer>
      </MainSection>
    </GridContainer>
  );
}
