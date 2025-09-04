import React, { useState, useEffect } from 'react';
import { Box, Text, Flex, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Badge } from '../../atoms/Badge';
import { ProgressBar } from '../../molecules/ProgressBar';
import { useUptime } from '../../shared/useUptime';
import { randomWalkStep, RandomWalkConfig } from '../../../lib/randomWalkUtils';
import { useTiming } from '../../shared/TimingContext';
import { DashboardData } from '../../../types/dashboard';

export interface NodeOverviewProps {
  data: DashboardData['nodeOverview'];
  ports: DashboardData['ports'];
}

const SectionContainer = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

const Divider = styled(Box, {
  marginTop: '$lg',
  paddingTop: '$lg',
  borderTop: '1px solid rgba(255, 255, 255, 0.05)',
});

const PortsList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  '& > *:not(:last-child)': {
    borderBottom: '1px solid rgba(255, 255, 255, 0.05)',
  },
});

const PortItem = styled(Flex, {
  width: '100%',
  paddingTop: '$md',
  paddingBottom: '$md',
  alignItems: 'center',
  justifyContent: 'space-between',
});

const PortInfo = styled(Flex, {
  width: 'auto',
  alignItems: 'center',
  gap: '$xs',
});

const PortNumber = styled(Text, {
  fontSize: '$3',
  fontFamily: 'monospace',
  color: '$muted',
});

const UptimeContainer = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
});

const UptimeLabel = styled(Text, {
  fontSize: '$2',
  letterSpacing: '$wide',
  color: '$muted',
  textTransform: 'uppercase',
});

const UptimeValue = styled(Text, {
  fontSize: '$3',
  color: '$light',
  fontFamily: 'monospace',
});

export const NodeOverview: React.FC<NodeOverviewProps> = ({ data, ports }) => {
  // Use live uptime counter starting at 69d 4h 20m
  const uptime = useUptime(69, 4, 20);

  // Random walk states for memory and CPU
  const [memoryValue, setMemoryValue] = useState(6.2); // 6.2GB ± 0.1GB
  const [cpuPercentage, setCpuPercentage] = useState(19); // 19% ± 2%

  const { peerTick, isActive } = useTiming();

  useEffect(() => {
    if (isActive) {
      // Update memory usage (6.2GB ± 0.1GB)
      setMemoryValue(prev => {
        const config: RandomWalkConfig = {
          target: 6.2,
          standardDeviation: 0.1,
          minValue: 5.8, // Reasonable lower bound
          maxValue: 6.6, // Reasonable upper bound
          precision: 1, // 1 decimal place for GB
          meanReversionStrength: 0.25,
          varianceScale: 0.15,
        };
        return randomWalkStep(prev, config);
      });

      // Update CPU percentage (19% ± 2%)
      setCpuPercentage(prev => {
        const config: RandomWalkConfig = {
          target: 19,
          standardDeviation: 2,
          minValue: 12, // Reasonable lower bound
          maxValue: 26, // Reasonable upper bound
          precision: 1, // 1 decimal place for percentage
          meanReversionStrength: 0.25,
          varianceScale: 0.15,
        };
        return randomWalkStep(prev, config);
      });
    }
  }, [peerTick, isActive]);

  // Calculate dynamic percentages
  const memoryPercentage = (memoryValue / data.memoryUsage.total) * 100;
  const diskPercentage = data.diskUsage.percentage; // Keep disk static

  // Port data for the ports section
  const portList = [
    { key: 'p2p', name: 'P2P', port: ports.p2p.port, status: ports.p2p.status },
    {
      key: 'jsonRpc',
      name: 'JSON-RPC',
      port: ports.jsonRpc.port,
      status: ports.jsonRpc.status,
    },
    {
      key: 'prometheus',
      name: 'Prometheus',
      port: ports.prometheus.port,
      status: ports.prometheus.status,
    },
    {
      key: 'beaconApi',
      name: 'Beacon API',
      port: ports.beaconApi.port,
      status: ports.beaconApi.status,
    },
  ];

  return (
    <Card
      title="Node Overview"
      css={{
        padding: '$lg',
        '@md': { padding: '$xl' },
      }}
    >
      {/* System Resources Section */}
      <SectionContainer>
        <ProgressBar
          label="Disk"
          value={diskPercentage}
          valueText={`${data.diskUsage.value} ${data.diskUsage.unit} of ${data.diskUsage.total} ${data.diskUsage.unit}`}
          showPercentage
          compact
        />

        <ProgressBar
          label="Memory"
          value={memoryPercentage}
          valueText={`${memoryValue} ${data.memoryUsage.unit} of ${data.memoryUsage.total} ${data.memoryUsage.unit}`}
          showPercentage
          compact
        />

        <ProgressBar
          label="CPU"
          value={cpuPercentage}
          valueText={`${cpuPercentage}% load average`}
          showPercentage
          compact
        />

        <UptimeContainer>
          <UptimeLabel>Uptime</UptimeLabel>
          <UptimeValue>{uptime.formatted}</UptimeValue>
        </UptimeContainer>
      </SectionContainer>

      {/* Ports Section */}
      <Divider>
        <Text variant="h5">Network Ports</Text>
        <PortsList>
          {portList.map(port => (
            <PortItem key={port.key}>
              <Text
                css={{
                  fontSize: '$3',
                  fontWeight: '$medium',
                  color: '$light',
                }}
              >
                {port.name}
              </Text>

              <PortInfo>
                <PortNumber>{port.port}</PortNumber>
                <Badge
                  variant={port.status === 'open' ? 'success' : 'error'}
                  size="sm"
                >
                  {port.status === 'open' ? 'Open' : 'Closed'}
                </Badge>
              </PortInfo>
            </PortItem>
          ))}
        </PortsList>
      </Divider>
    </Card>
  );
};
