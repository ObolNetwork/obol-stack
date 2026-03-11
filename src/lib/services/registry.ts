/**
 * 8004scan public API client — queries indexed ERC-8004 agent registrations.
 */

import type { Agent8004, Agent8004ListResponse } from '@/types/registry';

const SCAN_API = 'https://www.8004scan.io/api/v1/public';

export class RegistryClient {
  async listAgents(
    limit = 20,
    search?: string,
    protocol?: string,
    chainId = 84532
  ): Promise<Agent8004[]> {
    const params = new URLSearchParams({
      chainId: String(chainId),
      limit: String(limit),
      sortBy: 'created_at',
      sortOrder: 'desc',
    });

    if (search) params.set('search', search);
    if (protocol) params.set('protocol', protocol);

    const res = await fetch(`${SCAN_API}/agents?${params}`, {
      signal: AbortSignal.timeout(10000),
    });

    if (!res.ok) {
      throw new Error(`8004scan API error: ${res.status}`);
    }

    const json: Agent8004ListResponse = await res.json();
    return json.data || [];
  }
}
