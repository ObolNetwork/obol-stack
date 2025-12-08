/**
 * Utility functions for detecting and mapping Ethereum clients
 */

import {
  APP_TO_NODE_MODE,
  AppInstanceType,
  ClientNameType,
  NodeModeType,
} from '../../types/dashboard';

/**
 * Parse version string to extract version number
 * @param versionString The full version string
 * @returns Clean version number or the original string if parsing fails
 */
export function parseVersion(versionString: string): string {
  // Try to extract semantic version (e.g., v1.2.3)
  const versionMatch = versionString.match(/v?\d+\.\d+\.\d+/);
  if (versionMatch) {
    return versionMatch[0];
  }

  // Try to extract version after slash (e.g., Nethermind/v1.32.4)
  const parts = versionString.split('/');
  if (parts.length > 1 && parts[1]) {
    return parts[1];
  }

  return versionString;
}

/**
 * Get NodeModeType from app instance type
 * @param appType - The app instance type ('helios' | 'aztec' | 'ethereum')
 * @returns The corresponding NodeModeType
 */
export function getNodeModeFromAppInstance(
  appType: AppInstanceType
): NodeModeType {
  return APP_TO_NODE_MODE[appType];
}

/**
 * Client icon mapping by client name
 */
const CLIENT_ICONS: Partial<Record<ClientNameType, string>> = {
  reth: 'https://avatars.githubusercontent.com/u/97369466?s=48&v=4',
  geth: 'https://avatars.githubusercontent.com/u/6250754?s=200&v=4',
  nethermind: 'https://avatars.githubusercontent.com/u/43478154?s=200&v=4',
  besu: 'https://avatars.githubusercontent.com/u/7657900?s=48&v=4',
  erigon: 'https://avatars.githubusercontent.com/u/66788158?s=48&v=4',
  ethereumjs: 'https://avatars.githubusercontent.com/u/16297473?s=200&v=4',
  lighthouse: 'https://avatars.githubusercontent.com/u/22672004?s=48&v=4',
  prysm: 'https://avatars.githubusercontent.com/u/35349758?s=200&v=4',
  teku: 'https://avatars.githubusercontent.com/u/10818037?s=48&v=4',
  nimbus: 'https://avatars.githubusercontent.com/u/11767950?s=200&v=4',
  lodestar: 'https://avatars.githubusercontent.com/u/27474093?s=48&v=4',
  grandine: 'https://avatars.githubusercontent.com/u/53257991?s=48&v=4',
  aztec: 'https://avatars.githubusercontent.com/u/45291733?s=200&v=4',
  helios: 'https://avatars.githubusercontent.com/u/745163?s=200&v=4',
} as const;

/**
 * Get client icon URL by client name
 * @param clientName - The client name (ClientNameType)
 * @returns Icon URL string, or empty string if not found
 */
export function getClientIcon(clientName: ClientNameType): string {
  return CLIENT_ICONS[clientName] || '';
}
