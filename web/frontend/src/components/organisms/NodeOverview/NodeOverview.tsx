import React from 'react';
import { Box, Text, Flex, styled, theme } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { SectionTitle as CardTitle } from '../../atoms/SectionTitle';
import { PercentageChip } from '../../atoms/PercentageChip';
import { ProgressBar } from '../../molecules/ProgressBar';
import { NodeDisk, NodeMemory, NodeCpu, NodeUptime } from '@/icons';
import { DashboardData } from '../../../types/dashboard';
import { formatUptime } from '@/lib/utils/format-uptime';

export interface NodeOverviewProps {
  data: DashboardData['nodeOverview'];
  ports: DashboardData['ports'];
}

const NodeHeader = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$lg',
});

const NodeContent = styled(Box, {
  paddingTop: '$xs',
});

const MetricsGrid = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'repeat(4, 1fr)',
  gap: '$xxs',
  marginBottom: '$xl',

  // obol-ui's published media only defines @sm (≤768px) and @bp2, so the
  // tablet step is a raw query.
  '@media (max-width: 1200px)': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },

  '@sm': {
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
  showPercentageChip?: boolean;
}

const MetricCardContainer = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'flex-start',
  flex: '1 0 0',
  borderRadius: '$2',
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
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$semibold',
  lineHeight: '$normal',
  textTransform: 'uppercase',
});

const MetricValue = styled(Text, {
  color: '$white',
  fontFamily: '$fonts$body',
  fontSize: '$4',
  fontStyle: 'normal',
  fontWeight: '$semibold',
  lineHeight: '$base' /* 150% */,
});

const MetricSubtext = styled(Text, {
  color: '$body',
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$medium',
  lineHeight: '$base' /* 150% */,
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
  showPercentageChip = true,
}) => {
  return (
    <MetricCardContainer>
      <MetricHeader>
        <MetricHeaderLeft>
          <MetricIconContainer>{icon}</MetricIconContainer>
          <MetricLabel>{label}</MetricLabel>
        </MetricHeaderLeft>
        {showPercentageChip && percentage !== undefined && (
          <PercentageChip percentage={percentage} />
        )}
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

// Uppercase eyebrow label for metric groups — a label, not a heading.
// (Renamed from a local `SectionTitle` that collided with the shared atom.)
const MetricGroupLabel = styled(Text, {
  color: '$body',
  fontFamily: '$fonts$body',
  fontSize: '$1',
  fontStyle: 'normal',
  fontWeight: '$semibold',
  lineHeight: '$tall2' /* 280% */,
  textTransform: 'uppercase',
});

const PortsList = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'repeat(4, 1fr)',
  gap: '$xxs',

  // obol-ui's published media only defines @sm (≤768px) and @bp2, so the
  // tablet step is a raw query.
  '@media (max-width: 1200px)': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },

  '@sm': {
    gridTemplateColumns: '1fr',
  },
});

const PortItem = styled(Flex, {
  display: 'flex',
  flex: '1 0 0',
  padding: '$xs',
  alignItems: 'center',
  justifyContent: 'space-between',
  borderRadius: '$2',
  background: '$bg03',
});

const PortNameAndNumber = styled(Flex, {
  flexDirection: 'column',
  alignItems: 'flex-start',
  gap: '$xxxs',
});

const PortName = styled(Text, {
  color: '$body',
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$semibold',
  lineHeight: '$normal',
  textTransform: 'uppercase',
});

const PortNumber = styled(Text, {
  color: '$light',
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$medium',
  lineHeight: '$base' /* 150% */,
});

const PortStatus = styled(Text, {
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$medium',
  lineHeight: '$base' /* 150% */,
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

export const NodeOverview: React.FC<NodeOverviewProps> = ({ data, ports }) => {
  // Use real API data directly
  const diskUnavailable = data.diskUsage.unavailable === true;
  const diskPercentage = diskUnavailable ? 0 : data.diskUsage.percentage;
  const diskValue = diskUnavailable ? '—' : `${data.diskUsage.value} GB`;
  const diskSubtext = diskUnavailable
    ? 'no disk data'
    : `of ${data.diskUsage.total}GB`;
  const memoryUnavailable = data.memoryUsage.unavailable === true;
  const memoryPercentage = memoryUnavailable ? 0 : data.memoryUsage.percentage;
  const memoryValue = memoryUnavailable ? '—' : `${data.memoryUsage.value} GB`;
  const memorySubtext = memoryUnavailable
    ? 'no memory data'
    : `of ${data.memoryUsage.total}GB`;
  const cpuUnavailable = data.cpuLoad.unavailable === true;
  const cpuPercentage = cpuUnavailable ? 0 : data.cpuLoad.percentage;
  const cpuLabel = cpuUnavailable
    ? '—'
    : cpuPercentage % 1 === 0
      ? `${cpuPercentage}%`
      : `${cpuPercentage.toFixed(1)}%`;
  const uptimeUnavailable = data.uptime.unavailable === true;
  const uptimeBootUnknown = data.uptime.bootUnknown === true;
  const formattedUptime =
    uptimeUnavailable || uptimeBootUnknown
      ? '—'
      : formatUptime(data.uptime.hours);
  const uptimeSubtext = uptimeUnavailable
    ? 'metrics unavailable'
    : uptimeBootUnknown
      ? 'boot time unavailable'
      : 'continuous operation';

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
          icon={<NodeDisk size="xs" color={theme.colors.light.value} />}
          label="DISK"
          percentage={diskPercentage}
          value={diskValue}
          subtext={diskSubtext}
          showPercentageChip={!diskUnavailable}
          isExpanded={true}
        />

        <MetricCard
          icon={<NodeMemory size="xs" color={theme.colors.light.value} />}
          label="MEMORY"
          percentage={memoryPercentage}
          value={memoryValue}
          subtext={memorySubtext}
          showPercentageChip={!memoryUnavailable}
          isExpanded={true}
        />

        <MetricCard
          icon={<NodeCpu size="xs" color={theme.colors.light.value} />}
          label="CPU"
          percentage={cpuPercentage}
          value={cpuLabel}
          subtext="5m avg utilization"
          showPercentageChip={false}
          isExpanded={true}
        />

        <MetricCard
          icon={<NodeUptime size="xs" color={theme.colors.light.value} />}
          label="UPTIME"
          value={formattedUptime}
          subtext={uptimeSubtext}
          isExpanded={true}
        />
      </MetricsGrid>
    </NodeHeader>
  );

  return <Card css={{ padding: '$lg' }}>{headerContent}</Card>;
};
