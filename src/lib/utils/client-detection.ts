/**
 * Utility functions for detecting and mapping Ethereum clients
 */

import { NODE_MODES, NodeModeType } from '../../types/dashboard';

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
 * Client configuration interface
 */
export interface ClientConfig {
  name: string;
  endpoint: string;
  type: 'execution' | 'consensus';
  icon: string;
}

/**
 * Get available clients by mode
 * @param mode - The node mode NodeModeType
 * @returns Array of client configurations with name and endpoint
 */
export function getClients(mode: NodeModeType): ClientConfig[] {
  if (mode === NODE_MODES.FULL_MODE) {
    return [
      {
        name: 'Nethermind',
        endpoint: 'http://l1-full-node-execution.l1.svc.cluster.local:8545',
        type: 'execution',
        icon: 'https://avatars.githubusercontent.com/u/43478154?s=200&v=4',
      },
      {
        name: 'Nimbus',
        endpoint: 'http://l1-full-node-beacon.l1.svc.cluster.local:5052',
        type: 'consensus',
        icon: 'https://avatars.githubusercontent.com/u/11767950?s=200&v=4',
      },
    ];
  } else if (mode === NODE_MODES.LIGHT_MODE) {
    return [
      {
        name: 'Helios',
        endpoint: 'http://l1-light-client-helios.l1.svc.cluster.local:8545',
        type: 'execution',
        icon: 'https://avatars.githubusercontent.com/u/745163?s=200&v=4',
      },
    ];
  }

  return [];
}
