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
          {data.chains.map((chain, index) => (
            <NetworkStatus
              key={chain.chainName}
              chainData={chain}
              networkName={
                chain.chainName.charAt(0).toUpperCase() +
                chain.chainName.slice(1)
              }
              networkIcon={<ChainIcon chain={chain.chainName} size={24} />}
              defaultExpanded={index === 0}
            />
          ))}
          <NodeOverview data={data.nodeOverview} ports={data.ports} />
        </SectionContainer>
      </MainSection>
    </GridContainer>
  );
}
