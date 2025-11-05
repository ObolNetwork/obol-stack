export type IconSize = 'xxs' | 'xs' | 's' | 'm' | 'l' | 'xl' | 'xxl';

const sizeMap: Record<IconSize, number> = {
  xxs: 12,
  xs: 16,
  s: 24,
  m: 32,
  l: 40,
  xl: 48,
  xxl: 80,
};

export const getIconSize = (size: IconSize): number => sizeMap[size];

export { AddCircle } from './AddCircle';
export { Behind } from './Behind';
export { Bottleneck } from './Bottleneck';
export { Close } from './Close';
export { Healthy } from './Healthy';
export { HighUsage } from './HighUsage';
export { LowPeer } from './LowPeer';
export { NodeCpu } from './NodeCpu';
export { NodeDisk } from './NodeDisk';
export { NodeMemory } from './NodeMemory';
export { NodeUptime } from './NodeUptime';
export { Peers } from './Peers';
export { StalledSync } from './StalledSync';
export { LowConnection } from './LowConnection';
export { Up } from './Up';
