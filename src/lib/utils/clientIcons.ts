/**
 * Client icon mappings
 * Maps client names to their respective icon URLs
 */

export const CLIENT_ICONS = {
  // Ethereum clients
  nethermind:
    'https://raw.githubusercontent.com/NethermindEth/nethermind/master/nethermind.png',
  nimbus: 'https://nimbus.team/img/nimbus-logo.svg',
} as const;

/**
 * Get client icon URL by client name
 * @param clientName - Name of the client (case insensitive)
 * @returns Icon URL or undefined if not found
 */
export function getClientIcon(clientName: string): string | undefined {
  const normalizedName = clientName.toLowerCase();
  return CLIENT_ICONS[normalizedName as keyof typeof CLIENT_ICONS];
}

/**
 * Check if a client has an available icon
 * @param clientName - Name of the client (case insensitive)
 * @returns True if icon is available
 */
export function hasClientIcon(clientName: string): boolean {
  return getClientIcon(clientName) !== undefined;
}
