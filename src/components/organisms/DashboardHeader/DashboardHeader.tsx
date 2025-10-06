import { Box, Flex, Image } from '@obolnetwork/obol-ui';
import { WalletConnection } from '../../molecules/WalletConnection';

export function DashboardHeader() {
  return (
    <Flex css={{ justifyContent: 'space-between', alignItems: 'center' }}>
      <Box>
        <Image src="/obol-stack-logo.png" alt="obol-stack-logo" />
      </Box>

      <Flex css={{ gap: '$sm', alignItems: 'center', width: 'fit-content' }}>
        <WalletConnection />
      </Flex>
    </Flex>
  );
}
