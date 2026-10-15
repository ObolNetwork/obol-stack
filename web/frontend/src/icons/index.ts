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
export { Close } from './Close';
export { NodeCpu } from './NodeCpu';
export { NodeDisk } from './NodeDisk';
export { NodeMemory } from './NodeMemory';
export { NodeUptime } from './NodeUptime';
export { Peers } from './Peers';
export { LowConnection } from './LowConnection';
export { Up } from './Up';
