/**
 * Utility functions for detecting and mapping Ethereum clients
 */

import {
  APP_TO_NODE_MODE,
  AppInstanceType,
  NODE_MODES,
  NodeModeType,
} from '../../types/dashboard';
import { ClientType } from '../services/client-metrics';
import { config } from '../config/environment';

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
 * Client name and icon mapping by app instance type
 */
const APP_CLIENT_INFO: Record<ClientType, { name: string; icon: string }> = {
  helios: {
    name: 'Helios',
    icon: 'https://avatars.githubusercontent.com/u/745163?s=200&v=4',
  },
  aztec: {
    name: 'Archiver',
    icon: 'https://avatars.githubusercontent.com/u/47395224?s=200&v=4',
  },
  execution: {
    name: 'Nethermind',
    icon: 'https://avatars.githubusercontent.com/u/43478154?s=200&v=4',
  },

  consensus: {
    name: 'Nimbus',
    icon: 'https://avatars.githubusercontent.com/u/11767950?s=200&v=4',
  },
} as const;

/**
 * Get client name and icon by app instance type
 * @param appType - The app instance type ('helios' | 'aztec' | 'ethereum')
 * @returns Object with name and icon for the client
 */
export function getClientInfoByAppType(clientType: ClientType): {
  name: string;
  icon: string;
} {
  return APP_CLIENT_INFO[clientType];
}
