/**
 * Utility functions for detecting and mapping Ethereum clients
 */

import { NODE_MODES, NodeModeType } from '../../types/dashboard';

export interface ClientMapping {
  name: string;
  iconUrl: string;
}

// Map of known client patterns to their metadata
const CLIENT_MAPPINGS: Record<string, ClientMapping> = {
  nethermind: {
    name: 'Nethermind',
    iconUrl: 'https://avatars.githubusercontent.com/u/43478154?s=200&v=4',
  },
  geth: {
    name: 'Geth',
    iconUrl: 'https://geth.ethereum.org/img/geth-logo.png',
  },
  besu: {
    name: 'Besu',
    iconUrl:
      'https://raw.githubusercontent.com/hyperledger/besu-docs/main/static/img/logo.svg',
  },
  erigon: {
    name: 'Erigon',
    iconUrl:
      'https://raw.githubusercontent.com/ledgerwatch/erigon/main/docs/logo.png',
  },
  helios: {
    name: 'Helios',
    iconUrl: 'https://avatars.githubusercontent.com/u/745163?s=200&v=4',
  },
  nimbus: {
    name: 'Nimbus',
    iconUrl: 'https://avatars.githubusercontent.com/u/11767950?s=200&v=4',
  },
  lighthouse: {
    name: 'Lighthouse',
    iconUrl:
      'https://raw.githubusercontent.com/sigp/lighthouse/stable/book/src/imgs/lighthouse-logo.png',
  },
  prysm: {
    name: 'Prysm',
    iconUrl: 'https://docs.prylabs.network/img/prysm-logo.png',
  },
  teku: {
    name: 'Teku',
    iconUrl: 'https://docs.teku.consensys.net/img/logo.svg',
  },
  lodestar: {
    name: 'Lodestar',
    iconUrl:
      'https://raw.githubusercontent.com/ChainSafe/lodestar/stable/assets/lodestar-icon.png',
  },
};

/**
 * Detect client name and icon from version string
 * @param versionString The version string returned by the client
 * @returns ClientMapping with name and icon URL
 */
export function detectClient(versionString: string): ClientMapping {
  const lowerVersion = versionString.toLowerCase();

  // Check each known client pattern
  for (const [pattern, mapping] of Object.entries(CLIENT_MAPPINGS)) {
    if (lowerVersion.includes(pattern)) {
      return mapping;
    }
  }

  // Fallback for unknown clients
  return {
    name: versionString.split('/')[0] || 'Unknown',
    iconUrl: 'https://avatars.githubusercontent.com/u/745163?s=200&v=4', // Helios avatar as fallback
  };
}

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
