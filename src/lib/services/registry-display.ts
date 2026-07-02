import type { BadgeVariant } from '@/components/atoms/Badge';
import type { Agent8004 } from '@/types/registry';
import type { ChainName } from '@/types/dashboard';

export interface RegistryBadge {
  key: string;
  label: string;
  variant: BadgeVariant;
}

const DEFAULT_REGISTRY_CHAIN_ID = 1;

const REGISTRY_CHAIN_IDS: Partial<Record<ChainName, number>> = {
  mainnet: 1,
  sepolia: 11155111,
  hoodi: 560048,
  base: 8453,
  'base-sepolia': 84532,
};

const REGISTRY_CHAIN_LABELS: Record<number, string> = {
  1: 'Ethereum',
  11155111: 'Ethereum Sepolia',
  560048: 'Hoodi',
  8453: 'Base Mainnet',
  84532: 'Base Sepolia',
};

export function chainIdForRegistryFilter(
  chain?: ChainName
): number | undefined {
  if (!chain) return DEFAULT_REGISTRY_CHAIN_ID;
  return REGISTRY_CHAIN_IDS[chain];
}

export function registryChainLabel(chainId: number): string {
  return REGISTRY_CHAIN_LABELS[chainId] ?? `EIP-155 ${chainId}`;
}

export function registryChainBadges(agent: Agent8004): RegistryBadge[] {
  const badges: RegistryBadge[] = [
    {
      key: 'chain',
      label: registryChainLabel(agent.chain_id),
      variant: 'code',
    },
  ];

  if (agent.is_testnet) {
    badges.push({
      key: 'testnet',
      label: 'testnet',
      variant: 'yellow',
    });
  }

  return badges;
}

export function registryHealthBadge(
  status: string | null
): RegistryBadge | null {
  const normalized = status?.trim().toLowerCase();
  if (!normalized) return null;

  let variant: BadgeVariant = 'neutral';
  if (['healthy', 'live', 'online', 'synced'].includes(normalized)) {
    variant = 'green';
  } else if (['degraded', 'warning', 'unknown'].includes(normalized)) {
    variant = 'yellow';
  } else if (['offline', 'error', 'unhealthy'].includes(normalized)) {
    variant = 'red';
  }

  return {
    key: 'health',
    label: `health: ${normalized}`,
    variant,
  };
}

/** Names that carry no signal: bare addresses, hashes, or "Agent #123" fillers. */
const TRIVIAL_NAME = /^(0x[0-9a-f]{4,}|agent\s*#?\d*)$/i;

function looksLikeEndpointUrl(endpoint: string | undefined): boolean {
  if (!endpoint) return false;
  try {
    const url = new URL(endpoint);
    return (
      (url.protocol === 'https:' || url.protocol === 'http:') &&
      url.hostname.includes('.')
    );
  } catch {
    return false;
  }
}

/**
 * Heuristic quality score for an 8004scan registry agent. Higher is better.
 *
 * Signals: non-trivial name, real description, supported protocols, x402,
 * reachable-looking service endpoints, tools/skills, verification, health,
 * image, and 8004scan's own total_score / star_count.
 */
export function registryAgentQualityScore(agent: Agent8004): number {
  let score = 0;

  const name = agent.name?.trim() ?? '';
  if (name && !TRIVIAL_NAME.test(name)) score += 3;

  if ((agent.description?.trim().length ?? 0) > 20) score += 3;

  score += Math.min(agent.supported_protocols?.length ?? 0, 4);

  if (agent.x402_supported) score += 2;

  const services = Object.values(agent.services ?? {});
  const endpointCount = services.filter(service =>
    looksLikeEndpointUrl(service?.endpoint)
  ).length;
  score += Math.min(endpointCount, 3) * 2;

  const toolCount = services.reduce(
    (count, service) => count + (service?.tools?.length ?? 0),
    0
  );
  score += Math.min(toolCount, 5);

  if (agent.is_verified) score += 3;

  const health = agent.health_status?.trim().toLowerCase() ?? '';
  if (['healthy', 'live', 'online', 'synced'].includes(health)) score += 2;

  if (agent.image_url?.trim()) score += 1;

  if (Number.isFinite(agent.total_score) && agent.total_score > 0) {
    score += Math.min(Math.floor(agent.total_score / 10), 5);
  }
  if (Number.isFinite(agent.star_count) && agent.star_count > 0) {
    score += Math.min(agent.star_count, 5);
  }

  return score;
}

/**
 * Sort agents by quality score, descending. Stable: agents with equal scores
 * keep their original (API) relative order. Returns a new array.
 */
export function rankRegistryAgents<T extends Agent8004>(
  agents: readonly T[]
): T[] {
  return agents
    .map((agent, index) => ({
      agent,
      index,
      score: registryAgentQualityScore(agent),
    }))
    .sort((a, b) => b.score - a.score || a.index - b.index)
    .map(entry => entry.agent);
}

export function registryCapabilityBadges(
  agent: Agent8004,
  limit = 12
): RegistryBadge[] {
  const badges: RegistryBadge[] = [];
  const seen = new Set<string>();

  const add = (kind: string, value: string, variant: BadgeVariant) => {
    const clean = value.trim();
    if (!clean) return;

    const key = `${kind}:${clean.toLowerCase()}`;
    if (seen.has(key)) return;
    seen.add(key);

    badges.push({
      key,
      label: `${kind}: ${clean}`,
      variant,
    });
  };

  for (const category of agent.categories ?? []) {
    add('category', category, 'purple');
  }

  for (const tag of agent.tags ?? []) {
    add('tag', tag, 'neutral');
  }

  for (const service of Object.values(agent.services ?? {})) {
    for (const tool of service.tools ?? []) {
      add('tool', tool, 'yellow');
    }
  }

  if (badges.length <= limit) return badges;

  return [
    ...badges.slice(0, limit),
    {
      key: 'overflow',
      label: `+${badges.length - limit} more`,
      variant: 'code',
    },
  ];
}
