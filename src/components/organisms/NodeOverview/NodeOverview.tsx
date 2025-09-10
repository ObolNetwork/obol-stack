import React, { useState, useEffect } from 'react';
import { Box, Text, Flex, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { PercentageChip } from '../../atoms/PercentageChip';
import { ProgressBar } from '../../molecules/ProgressBar';
import {
  DiskIcon,
  MemoryIcon,
  CpuIcon,
  UptimeIcon,
} from '../../icons/hardware';
import { useUptime } from '../../shared/useUptime';
import { randomWalkStep, RandomWalkConfig } from '../../../lib/randomWalkUtils';
import { useTiming } from '../../shared/TimingContext';
import { DashboardData } from '../../../types/dashboard';

export interface NodeOverviewProps {
  data: DashboardData['nodeOverview'];
  ports: DashboardData['ports'];
}

const NodeHeader = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$lg',
});

const CardTitle = styled(Text, {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
});

const NodeContent = styled(Box, {
  paddingTop: '$xs',
});

const MetricsGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'repeat(4, 1fr)',
  gap: '$lg',
  marginBottom: '$xl',

  '@media (max-width: 1200px)': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },

  '@media (max-width: 768px)': {
    gridTemplateColumns: '1fr',
  },
});

// Internal MetricCard component and interface
interface MetricCardProps {
  icon: React.ReactNode;
  label: string;
  percentage: number;
  value: string;
  subtext: string;
  isExpanded: boolean;
}

const MetricCardContainer = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'flex-start',
  flex: '1 0 0',
  borderRadius: '8px',
  background: '#182D32', // var(--Backgrounds-03, #182D32)
  gap: '$sm',
});

const MetricHeader = styled(Flex, {
  display: 'flex',
  padding: '12px',
  justifyContent: 'space-between',
  alignItems: 'center',
  alignSelf: 'stretch',
});

const MetricHeaderLeft = styled(Flex, {
  alignItems: 'center',
  gap: '$xxs',
});

const MetricIconContainer = styled(Box, {
  width: '$lg',
  height: '$lg',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
});

const MetricLabel = styled(Text, {
  color: '#97B2B8', // var(--Text-Body, #97B2B8)
  fontFamily: '"DM Sans"',
  fontSize: '12px',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: 'normal',
  textTransform: 'uppercase',
});

const MetricValue = styled(Text, {
  color: '#FFF',
  fontFamily: '"DM Sans"',
  fontSize: '16px',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: '24px' /* 150% */,
  padding: '0 12px',
});

const MetricSubtext = styled(Text, {
  color: '#97B2B8', // var(--Text-Body, #97B2B8)
  fontFamily: '"DM Sans"',
  fontSize: '12px',
  fontStyle: 'normal',
  fontWeight: '500',
  lineHeight: '18px' /* 150% */,
  padding: '0 12px 12px 12px',
});

const ProgressBarContainer = styled(Box, {
  paddingLeft: '8px',
  paddingRight: '8px',
  width: '100%',
});

const MetricCard: React.FC<MetricCardProps> = ({
  icon,
  label,
  percentage,
  value,
  subtext,
  isExpanded,
}) => {
  return (
    <MetricCardContainer>
      <MetricHeader>
        <MetricHeaderLeft>
          <MetricIconContainer>{icon}</MetricIconContainer>
          <MetricLabel>{label}</MetricLabel>
        </MetricHeaderLeft>
        <PercentageChip percentage={percentage} />
      </MetricHeader>
      {isExpanded ? (
        <>
          <MetricValue>{value}</MetricValue>
          <ProgressBarContainer>
            <ProgressBar value={percentage} />
          </ProgressBarContainer>
          <MetricSubtext>{subtext}</MetricSubtext>
        </>
      ) : (
        <ProgressBarContainer>
          <ProgressBar value={percentage} />
        </ProgressBarContainer>
      )}
    </MetricCardContainer>
  );
};

const SectionContainer = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

const SectionTitle = styled(Text, {
  color: '#97B2B8', // var(--Text-Body, #97B2B8)
  fontFamily: '"DM Sans"',
  fontSize: '10px',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: '28px' /* 280% */,
  textTransform: 'uppercase',
});

const PortsList = styled(Box, {
  display: 'flex',
  flexWrap: 'wrap',
  gap: '$sm',
});

const PortItem = styled(Flex, {
  display: 'flex',
  flex: '1 1 calc(50% - 8px)',
  minWidth: '200px',
  maxWidth: '171px',
  padding: '12px',
  alignItems: 'center',
  justifyContent: 'space-between',
  borderRadius: '8px',
  background: '#182D32', // var(--Backgrounds-03, #182D32)
});

const PortNameAndNumber = styled(Flex, {
  flexDirection: 'column',
  alignItems: 'flex-start',
  gap: '4px',
});

const PortName = styled(Text, {
  color: '#97B2B8', // var(--Text-Body, #97B2B8)
  fontFamily: '"DM Sans"',
  fontSize: '12px',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: 'normal',
  textTransform: 'uppercase',
});

const PortNumber = styled(Text, {
  color: '#E1E9EB', // var(--Text-Light, #E1E9EB)
  fontFamily: '"DM Sans"',
  fontSize: '12px',
  fontStyle: 'normal',
  fontWeight: '500',
  lineHeight: '18px' /* 150% */,
});

const PortStatus = styled(Text, {
  fontFamily: '"DM Sans"',
  fontSize: '12px',
  fontStyle: 'normal',
  fontWeight: '500',
  lineHeight: '18px' /* 150% */,
  padding: '$1 $2',
  borderRadius: '$1',

  variants: {
    status: {
      open: {
        color: '#2FE4AB',
      },
      closed: {
        color: '#FF5757',
      },
    },
  },
});

export const NodeOverview: React.FC<NodeOverviewProps> = ({ data, ports }) => {
  const [isExpanded, setIsExpanded] = useState(true);
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

  const headerContent = (
    <NodeHeader>
      <CardTitle>Node Overview</CardTitle>
      <MetricsGrid>
        <MetricCard
          icon={<DiskIcon width={20} height={20} fill="#E1E9EB" />}
          label="DISK"
          percentage={diskPercentage}
          value={`${data.diskUsage.value} GB`}
          subtext={`of ${data.diskUsage.total}GB`}
          isExpanded={isExpanded}
        />

        <MetricCard
          icon={<MemoryIcon width={20} height={20} fill="#E1E9EB" />}
          label="MEMORY"
          percentage={memoryPercentage}
          value={`${memoryValue} GB`}
          subtext={`of ${data.memoryUsage.total}GB`}
          isExpanded={isExpanded}
        />

        <MetricCard
          icon={<CpuIcon width={20} height={20} fill="#E1E9EB" />}
          label="CPU"
          percentage={cpuPercentage}
          value={`${cpuPercentage}%`}
          subtext="load average"
          isExpanded={isExpanded}
        />

        <MetricCard
          icon={<UptimeIcon width={20} height={20} fill="#E1E9EB" />}
          label="UPTIME"
          percentage={8}
          value={uptime.formatted}
          subtext="continuous operation"
          isExpanded={isExpanded}
        />
      </MetricsGrid>
    </NodeHeader>
  );

  return (
    <Card
      css={{
        position: 'relative',
        overflow: 'hidden',
        padding: '0',
      }}
    >
      <Accordion
        header={headerContent}
        defaultExpanded={true}
        onToggle={expanded => {
          setIsExpanded(expanded);
        }}
      >
        <NodeContent>
          {/* Ports Section */}
          <SectionContainer>
            <SectionTitle>PORTS</SectionTitle>
            <PortsList>
              {portList.map(port => (
                <PortItem key={port.key}>
                  <PortNameAndNumber>
                    <PortName>{port.name}</PortName>
                    <PortNumber>{port.port}</PortNumber>
                  </PortNameAndNumber>

                  <PortStatus status={port.status}>
                    {port.status === 'open' ? 'Open' : 'Closed'}
                  </PortStatus>
                </PortItem>
              ))}
            </PortsList>
          </SectionContainer>
        </NodeContent>
      </Accordion>
    </Card>
  );
};
