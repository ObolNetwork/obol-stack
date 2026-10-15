/**
 * 8004scan public API client — queries indexed ERC-8004 agent registrations.
 */

import type { Agent8004, Agent8004ListResponse } from '@/types/registry';

const SCAN_API = 'https://www.8004scan.io/api/v1/public';

export class RegistryClient {
  async listAgents(
    limit = 20,
    page = 1,
    search?: string,
    protocol?: string,
    chainId = 1,
    x402Supported?: boolean
  ): Promise<{ agents: Agent8004[]; total: number; hasMore: boolean }> {
    const params = new URLSearchParams({
      chainId: String(chainId),
      limit: String(limit),
      page: String(page),
      sortBy: 'created_at',
      sortOrder: 'desc',
    });

    if (search) params.set('search', search);
    if (protocol) params.set('protocol', protocol);
    if (x402Supported) params.set('x402_supported', 'true');

    const res = await fetch(`${SCAN_API}/agents?${params}`, {
      signal: AbortSignal.timeout(10000),
    });

    if (!res.ok) {
      throw new Error(`8004scan API error: ${res.status}`);
    }

    const json: Agent8004ListResponse = await res.json();
    return {
      agents: json.data || [],
      total: json.meta?.pagination?.total ?? 0,
      hasMore: json.meta?.pagination?.hasMore ?? false,
    };
  }

  /** Paginate through 8004scan until all agents for a chain are loaded. */
  async listAllAgents(
    chainId: number,
    options?: {
      search?: string;
      protocol?: string;
      x402Supported?: boolean;
      batchSize?: number;
    }
  ): Promise<{ agents: Agent8004[]; total: number }> {
    const batchSize = options?.batchSize ?? 100;
    const agents: Agent8004[] = [];
    let page = 1;
    let total = 0;
    let hasMore = true;

    while (hasMore) {
      const batch = await this.listAgents(
        batchSize,
        page,
        options?.search,
        options?.protocol,
        chainId,
        options?.x402Supported
      );
      agents.push(...batch.agents);
      total = batch.total;
      hasMore = batch.hasMore && batch.agents.length > 0;
      page += 1;
    }

    return { agents, total };
  }
}
