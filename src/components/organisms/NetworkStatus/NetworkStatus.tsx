import { Box, Flex, Text } from '@obolnetwork/obol-ui';
import { Accordion } from '../../atoms/Accordion';

const data = {
  network: 'Ethereum Mainnet',
  status: 'synced' as const,
  block: 18974341,
  headslot: 18974341,
  lastBlock: '6s',
  distanceToJustify: 18974341,
  peers: 56,
  finalizedEpoch: {
    epoch: 254170,
    slot: '8,113,440',
  },
  blockHeight: {
    height: 18972341,
    description: 'Latest execution block',
  },
  nodeMode: {
    mode: 'Full Node',
    description: 'Complete Chain Data',
  },
  participation: {
    rate: 97.8,
    description: 'Recent Attestation Rate',
  },
  clients: {
    execution: {
      name: 'Geth',
      type: 'Execution',
      port: 22,
      peers: 56,
    },
    consensus: {
      name: 'Prysm',
      type: 'Consensus',
      port: 22,
      peers: 21,
    },
  },
};

const StatusBadge = ({
  status,
}: {
  status: 'synced' | 'syncing' | 'error';
}) => {
  const getStatusColor = (status: string) => {
    switch (status) {
      case 'synced':
        return '#2FE4AB'; // obol green
      case 'syncing':
        return '#FABA5A'; // obol orange
      case 'error':
        return '#FF6B6B'; // red
      default:
        return '#97B2B8'; // muted
    }
  };

  const color = getStatusColor(status);

  return (
    <Box
      css={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '8px',
        padding: '8px 16px',
        backgroundColor: 'rgba(47, 228, 171, 0.1)',
        border: `1px solid ${color}`,
        borderRadius: '8px',
      }}
    >
      <Box
        css={{
          width: '8px',
          height: '8px',
          backgroundColor: color,
          borderRadius: '50%',
        }}
      />
      <Text
        css={{
          color: color,
          fontSize: '14px',
          fontWeight: '500',
          textTransform: 'capitalize',
        }}
      >
        {status}
      </Text>
    </Box>
  );
};

export function NetworkStatus() {
  return (
    <Accordion.Root type="single" collapsible>
      <Accordion.Item value="roles">
        <Accordion.Header>
          <Accordion.Trigger>
            <Flex
              css={{ justifyContent: 'space-between', alignItems: 'center' }}
            >
              <Text variant="h5">Ethereum Mainnet</Text>
              <StatusBadge status={data.status} />
            </Flex>
          </Accordion.Trigger>
        </Accordion.Header>
        <Accordion.Content></Accordion.Content>
      </Accordion.Item>
    </Accordion.Root>
  );
}
