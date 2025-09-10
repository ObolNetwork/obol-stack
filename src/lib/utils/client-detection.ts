/**
 * Utility functions for detecting and mapping Ethereum clients
 */

export interface ClientMapping {
  name: string;
  role: 'execution' | 'consensus' | 'validator';
  iconUrl: string;
}

// Map of known client patterns to their metadata
const CLIENT_MAPPINGS: Record<string, ClientMapping> = {
  // Execution clients
  nethermind: {
    name: 'Nethermind',
    role: 'execution',
    iconUrl:
      'https://raw.githubusercontent.com/NethermindEth/nethermind/master/nethermind.png',
  },
  geth: {
    name: 'Geth',
    role: 'execution',
    iconUrl: 'https://geth.ethereum.org/img/geth-logo.png',
  },
  besu: {
    name: 'Besu',
    role: 'execution',
    iconUrl:
      'https://raw.githubusercontent.com/hyperledger/besu-docs/main/static/img/logo.svg',
  },
  erigon: {
    name: 'Erigon',
    role: 'execution',
    iconUrl:
      'https://raw.githubusercontent.com/ledgerwatch/erigon/main/docs/logo.png',
  },

  // Consensus clients
  nimbus: {
    name: 'Nimbus',
    role: 'consensus',
    iconUrl: 'https://nimbus.team/img/nimbus-logo.svg',
  },
  lighthouse: {
    name: 'Lighthouse',
    role: 'consensus',
    iconUrl:
      'https://raw.githubusercontent.com/sigp/lighthouse/stable/book/src/imgs/lighthouse-logo.png',
  },
  prysm: {
    name: 'Prysm',
    role: 'consensus',
    iconUrl: 'https://docs.prylabs.network/img/prysm-logo.png',
  },
  teku: {
    name: 'Teku',
    role: 'consensus',
    iconUrl: 'https://docs.teku.consensys.net/img/logo.svg',
  },
  lodestar: {
    name: 'Lodestar',
    role: 'consensus',
    iconUrl:
      'https://raw.githubusercontent.com/ChainSafe/lodestar/stable/assets/lodestar-icon.png',
  },
};

/**
 * Detect client type from version string
 * @param versionString The version string returned by the client
 * @returns ClientMapping with name, role, and icon URL
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
  // Try to guess role based on common patterns
  if (lowerVersion.includes('execution') || lowerVersion.includes('evm')) {
    return {
      name: versionString.split('/')[0] || 'Unknown',
      role: 'execution',
      iconUrl:
        'https://ethereum.org/static/a183661dd70e0e5c70689a0ec95ef0ba/31987/eth-diamond-purple.png',
    };
  }

  if (lowerVersion.includes('beacon') || lowerVersion.includes('consensus')) {
    return {
      name: versionString.split('/')[0] || 'Unknown',
      role: 'consensus',
      iconUrl:
        'https://ethereum.org/static/a183661dd70e0e5c70689a0ec95ef0ba/31987/eth-diamond-purple.png',
    };
  }

  // Default fallback
  return {
    name: versionString.split('/')[0] || 'Unknown',
    role: 'execution',
    iconUrl:
      'https://ethereum.org/static/a183661dd70e0e5c70689a0ec95ef0ba/31987/eth-diamond-purple.png',
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
