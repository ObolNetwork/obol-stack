import { Flex, Image } from '@obolnetwork/obol-ui';

export function DashboardHeader() {
  return (
    <Flex css={{ justifyContent: 'space-between', alignItems: 'center' }}>
      <Image src="/obol-stack-logo.png" alt="obol-stack-logo" />
      {/* <Box>
          <Text>Obol Stack</Text>
        </Box> */}
    </Flex>
  );
}
