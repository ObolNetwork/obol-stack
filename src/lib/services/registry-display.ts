import type { BadgeVariant } from '@/components/atoms/Badge';
import type { Agent8004 } from '@/types/registry';
import type { ChainName } from '@/types/dashboard';

export interface RegistryBadge {
  key: string;
  label: string;
  variant: BadgeVariant;
}

const DEFAULT_REGISTRY_CHAIN_ID = 84532;

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
