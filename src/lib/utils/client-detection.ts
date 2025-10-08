/**
 * Utility functions for detecting and mapping Ethereum clients
 */

import { NODE_MODES, NodeModeType } from '../../types/dashboard';
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
 * Client configuration interface
 */
export interface ClientConfig {
  name: string;
  endpoint: string;
  type: ClientType;
  icon: string;
  beaconUrl?: string;
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
        endpoint: config.fullNodeExecutionUrl,
        type: 'execution',
        icon: 'https://avatars.githubusercontent.com/u/43478154?s=200&v=4',
      },
      {
        name: 'Nimbus',
        endpoint: config.fullNodeBeaconUrl,
        type: 'consensus',
        icon: 'https://avatars.githubusercontent.com/u/11767950?s=200&v=4',
      },
    ];
  } else if (mode === NODE_MODES.LIGHT_MODE) {
    return [
      {
        name: 'Helios',
        endpoint: config.heliosClientUrl,
        type: 'helios',
        icon: 'https://avatars.githubusercontent.com/u/745163?s=200&v=4',
      },
    ];
  } else if (mode === NODE_MODES.AZTEC_FULLNODE) {
    return [
      {
        name: 'Archiver',
        endpoint: config.aztecFullNodeUrl,
        type: 'aztec',
        icon: 'https://avatars.githubusercontent.com/u/47395224?s=200&v=4', // Aztec org avatar
      },
    ];
  } else if (mode === NODE_MODES.AZTEC_SEQUENCER) {
    return [
      {
        name: 'Sequencer',
        endpoint: config.aztecSequencerUrl,
        type: 'aztec',
        icon: 'https://avatars.githubusercontent.com/u/47395224?s=200&v=4',
      },
    ];
  } else if (mode === NODE_MODES.AZTEC_PROVER) {
    return [
      {
        name: 'Prover',
        endpoint: config.aztecProverNodeUrl,
        type: 'aztec',
        icon: 'https://avatars.githubusercontent.com/u/47395224?s=200&v=4',
      },
      {
        name: 'Broker',
        endpoint: config.aztecProverBrokerUrl,
        type: 'aztec',
        icon: 'https://avatars.githubusercontent.com/u/47395224?s=200&v=4',
      },
      {
        name: 'Agent',
        endpoint: config.aztecProverAgentUrl,
        type: 'aztec',
        icon: 'https://avatars.githubusercontent.com/u/47395224?s=200&v=4',
      },
    ];
  }

  return [];
}
