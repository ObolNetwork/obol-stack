// Layout components
export { Header } from './layout/Header';
export { Footer } from './layout/Footer';
export { FooterBar } from './layout/FooterBar';

// Metric components (Legacy)
export { MetricCard } from './metrics/MetricCard';
export { NetworkStatus } from './metrics/NetworkStatus';
export { NodeOverview } from './metrics/NodeOverview';
export { ClientStatus } from './metrics/ClientStatus';

// Metric components (Compact Design)
export { EthereumSummaryCard } from './metrics/EthereumSummaryCard';
export { ClientMiniCards } from './metrics/ClientMiniCards';
export { NodeOverviewCompact } from './metrics/NodeOverviewCompact';

// Monitoring components
export { PortStatus } from './monitoring/PortStatus';
export { PortsList } from './monitoring/PortsList';

// Shared components (Legacy)
export { StatusIndicator } from './shared/StatusIndicator';
export { ProgressBar } from './shared/ProgressBar';

// Shared components (Compact Design)
export { Card } from './shared/Card';
export { Badge } from './shared/Badge';
export { Pill } from './shared/Pill';
export { ThinProgress } from './shared/ThinProgress';
export { MetricItem, InlineMetaItem, InlineMetaSeparator } from './shared/MetricItem';
export { AnimatedNumber } from './shared/AnimatedNumber';
export { RandomWalkNumber } from './shared/RandomWalkNumber';
export { ProgressSweep } from './shared/ProgressSweep';
export { TimingProvider, useTiming } from './shared/TimingContext';
export { useUptime } from './shared/useUptime';
export { ParticipationNumber } from './shared/ParticipationNumber';

// Shared utilities
export { randomWalkStep, generateNormalRandom, type RandomWalkConfig } from './shared/randomWalkUtils';