import { Box, Flex, Image } from '@obolnetwork/obol-ui';

interface HeaderProps {
  network: 'testnet' | 'mainnet';
}
export function DashboardHeader({ network }: HeaderProps) {
  return (
    <Flex css={{ justifyContent: 'space-between', alignItems: 'center' }}>
      <Box>
        <Image src="/obol-stack-logo.png" alt="obol-stack-logo" />
      </Box>

      <Flex
        css={{ gap: '$sm', itemsAlign: 'center', width: 'fit-content' }}
      ></Flex>
    </Flex>
  );
}
