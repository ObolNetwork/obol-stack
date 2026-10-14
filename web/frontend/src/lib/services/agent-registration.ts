export interface AgentRegistrationRef {
  agentId: number | string;
  agentRegistry: string;
}

export interface ParsedAgentRegistry {
  caip2Network: string;
  chainId: number;
  chainLabel: string;
  registryAddress: string;
  explorerBaseUrl?: string;
}

const EIP155_CHAINS: Record<
  number,
  { label: string; explorerBaseUrl: string }
> = {
  1: { label: 'Ethereum Mainnet', explorerBaseUrl: 'https://etherscan.io' },
  8453: { label: 'Base', explorerBaseUrl: 'https://basescan.org' },
  84532: {
    label: 'Base Sepolia',
    explorerBaseUrl: 'https://sepolia.basescan.org',
  },
};

export function parseAgentRegistry(
  agentRegistry: string
): ParsedAgentRegistry | null {
  const match = agentRegistry.match(/^eip155:(\d+):(0x[a-fA-F0-9]{40})$/);
  if (!match) return null;

  const chainId = Number(match[1]);
  if (!Number.isSafeInteger(chainId)) return null;

  const chain = EIP155_CHAINS[chainId];
  return {
    caip2Network: `eip155:${chainId}`,
    chainId,
    chainLabel: chain?.label ?? `eip155:${chainId}`,
    registryAddress: match[2],
    explorerBaseUrl: chain?.explorerBaseUrl,
  };
}

export function agentRegistrationLabel(
  registration: AgentRegistrationRef
): string {
  const parsed = parseAgentRegistry(registration.agentRegistry);
  const chainLabel = parsed?.chainLabel ?? registration.agentRegistry;
  return `Agent #${registration.agentId} on ${chainLabel}`;
}

export function agentRegistrationExplorerUrl(
  registration: AgentRegistrationRef
): string | null {
  const parsed = parseAgentRegistry(registration.agentRegistry);
  if (!parsed?.explorerBaseUrl) return null;

  return `${parsed.explorerBaseUrl}/nft/${parsed.registryAddress}/${encodeURIComponent(String(registration.agentId))}`;
}
