import React, { useState } from 'react';
import { Box, Text, Flex, styled, theme } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { PercentageChip } from '../../atoms/PercentageChip';
import { ProgressBar } from '../../molecules/ProgressBar';
import { Icon } from '../../atoms/Icon';
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
  gap: '$xxs',
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
  percentage?: number;
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
  background: '$bg03',
});

const MetricHeader = styled(Flex, {
  display: 'flex',
  paddingTop: '$xs',
  paddingRight: '$xs',
  paddingLeft: '$xs',
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
  color: '$body',
  fontFamily: '"DM Sans"',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: 'normal',
  textTransform: 'uppercase',
});

const MetricValue = styled(Text, {
  color: '$white',
  fontFamily: '"DM Sans"',
  fontSize: '$4',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: '24px' /* 150% */,
});

const MetricSubtext = styled(Text, {
  color: '$body',
  fontFamily: '"DM Sans"',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '500',
  lineHeight: '18px' /* 150% */,
  paddingTop: '$xxs',
  paddingBottom: '$xxs',
});

const ProgressBarContainer = styled(Box, {
  width: '100%',
});

const MetricContentContainer = styled(Flex, {
  paddingTop: '$xs',
  paddingLeft: '$xs',
  paddingRight: '$xs',
  flexDirection: 'column',
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
        {percentage !== undefined && <PercentageChip percentage={percentage} />}
      </MetricHeader>
      {isExpanded ? (
        <MetricContentContainer>
          <MetricValue>{value}</MetricValue>
          {percentage !== undefined && (
            <ProgressBarContainer>
              <ProgressBar value={percentage} />
            </ProgressBarContainer>
          )}
          <MetricSubtext>{subtext}</MetricSubtext>
        </MetricContentContainer>
      ) : (
        <MetricContentContainer>
          {percentage !== undefined && (
            <ProgressBarContainer>
              <ProgressBar value={percentage} />
            </ProgressBarContainer>
          )}
        </MetricContentContainer>
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
  color: '$body',
  fontFamily: '"DM Sans"',
  fontSize: '$1',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: '28px' /* 280% */,
  textTransform: 'uppercase',
});

const PortsList = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'repeat(4, 1fr)',
  gap: '$xxs',

  '@media (max-width: 1200px)': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },

  '@media (max-width: 768px)': {
    gridTemplateColumns: '1fr',
  },
});

const PortItem = styled(Flex, {
  display: 'flex',
  flex: '1 0 0',
  padding: '12px',
  alignItems: 'center',
  justifyContent: 'space-between',
  borderRadius: '8px',
  background: '$bg03',
});

const PortNameAndNumber = styled(Flex, {
  flexDirection: 'column',
  alignItems: 'flex-start',
  gap: '4px',
});

const PortName = styled(Text, {
  color: '$body',
  fontFamily: '"DM Sans"',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: 'normal',
  textTransform: 'uppercase',
});

const PortNumber = styled(Text, {
  color: '$light',
  fontFamily: '"DM Sans"',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '500',
  lineHeight: '18px' /* 150% */,
});

const PortStatus = styled(Text, {
  fontFamily: '"DM Sans"',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '500',
  lineHeight: '18px' /* 150% */,
  padding: '$1 $2',
  borderRadius: '$1',

  variants: {
    status: {
      open: {
        color: '$obolGreen',
      },
      closed: {
        color: '$test',
      },
    },
  },
});

// Helper function to format uptime hours into readable format
const formatUptime = (hours: number): string => {
  const days = Math.floor(hours / 24);
  const remainingHours = hours % 24;

  if (days > 0) {
    return `${days}d ${remainingHours}h`;
  }
  return `${remainingHours}h`;
};

export const NodeOverview: React.FC<NodeOverviewProps> = ({ data, ports }) => {
  const [isExpanded, setIsExpanded] = useState(true);

  // Use real API data directly
  const diskPercentage = data.diskUsage.percentage;
  const memoryPercentage = data.memoryUsage.percentage;
  const cpuPercentage = data.cpuLoad.percentage;
  const formattedUptime = formatUptime(data.uptime.hours);

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
          icon={
            <Icon name="node-disk" size="xs" color={theme.colors.light.value} />
          }
          label="DISK"
          percentage={diskPercentage}
          value={`${data.diskUsage.value} GB`}
          subtext={`of ${data.diskUsage.total}GB`}
          isExpanded={isExpanded}
        />

        <MetricCard
          icon={
            <Icon
              name="node-memory"
              size="xs"
              color={theme.colors.light.value}
            />
          }
          label="MEMORY"
          percentage={memoryPercentage}
          value={`${data.memoryUsage.value} GB`}
          subtext={`of ${data.memoryUsage.total}GB`}
          isExpanded={isExpanded}
        />

        <MetricCard
          icon={
            <Icon name="node-cpu" size="xs" color={theme.colors.light.value} />
          }
          label="CPU"
          percentage={cpuPercentage}
          value={`${cpuPercentage}%`}
          subtext="load average"
          isExpanded={isExpanded}
        />

        <MetricCard
          icon={
            <Icon
              name="node-uptime"
              size="xs"
              color={theme.colors.light.value}
            />
          }
          label="UPTIME"
          value={formattedUptime}
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
