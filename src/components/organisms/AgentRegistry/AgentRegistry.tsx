'use client';

import { Box, Flex, Text, Link, styled } from '@obolnetwork/obol-ui';
import { Accordion } from '../../molecules/Accordion';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { Badge } from '../../atoms/Badge';
import { Card } from '../../atoms/Card';
import { ChainSelector } from '../../atoms/ChainSelector';
import { SectionTitle } from '../../atoms/SectionTitle';
import { ItemCard } from '../../atoms/ItemCard';
import { SearchInput } from '../../atoms/SearchInput';
import { FilterChip } from '../../atoms/FilterChip';
import { Avatar } from '../../atoms/Avatar';
import { EmptyMessage } from '../../atoms/EmptyMessage';
import { Pagination } from '../../atoms/Pagination';
import { useRegistryAgents } from '../../../hooks/useRegistryAgents';
import { useAgentFilters } from '../../../hooks/useAgentFilters';
import { useSearch } from '../../../hooks/useSearch';
import { truncateAddress } from '../../../utils/address';
import type { Agent8004 } from '@/types/registry';
import {
  registryCapabilityBadges,
  registryChainBadges,
  registryHealthBadge,
} from '@/lib/services/registry-display';

const AvatarStack = styled('div', {
  display: 'flex',
  alignItems: 'center',
  flexShrink: 0,
});

const AvatarCircle = styled('div', {
  width: 28,
  height: 28,
  borderRadius: '50%',
  border: '2px solid $bg02',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  fontSize: '10px',
  fontWeight: '$semibold',
  flexShrink: 0,
});

const SEEDS = [
  { letter: 'A', bg: 'rgba(47, 228, 171, 0.25)', color: '#2FE4AB' },
  { letter: 'K', bg: 'rgba(60, 210, 221, 0.25)', color: '#3CD2DD' },
  { letter: 'M', bg: 'rgba(145, 103, 228, 0.25)', color: '#9167E4' },
  { letter: 'B', bg: 'rgba(250, 186, 90, 0.25)', color: '#FABA5A' },
  { letter: 'S', bg: 'rgba(182, 234, 92, 0.25)', color: '#B6EA5C' },
  { letter: 'H', bg: 'rgba(221, 96, 60, 0.25)', color: '#DD603C' },
  { letter: 'P', bg: 'rgba(47, 228, 171, 0.25)', color: '#2FE4AB' },
  { letter: 'C', bg: 'rgba(60, 210, 221, 0.25)', color: '#3CD2DD' },
];

const AgentList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  paddingTop: '$lg',
});

const ToolBar = styled(Flex, {
  width: 'auto',
  gap: '$xs',
  flexWrap: 'wrap',
});

const AgentTopRow = styled(Flex, {
  alignItems: 'flex-start',
  justifyContent: 'space-between',
  gap: '$xs',
  flexWrap: 'wrap',
  marginBottom: '$xxs',
});

const AgentName = styled(Text, {
  fontSize: '$3',
  fontWeight: '$semibold',
  color: '$light',
});

const AgentId = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  fontFamily: 'monospace',
});

const Description = styled(Text, {
  fontSize: '$2',
  color: '$body',
  lineHeight: 1.4,
  display: '-webkit-box',
  WebkitLineClamp: 2,
  WebkitBoxOrient: 'vertical',
  overflow: 'hidden',
  marginBottom: '$xs',
});

const MetaRow = styled(Flex, {
  gap: '$xs',
  flexWrap: 'wrap',
  marginTop: '$xxs',
});

const CapabilityRow = styled(Flex, {
  gap: '$xxs',
  flexWrap: 'wrap',
  marginTop: '$xs',
});

const OwnerAddr = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  fontFamily: 'monospace',
});

const EndpointLink = styled(Link, {
  fontSize: '$1',
  fontFamily: 'monospace',
});

function AgentCardItem({ agent }: { agent: Agent8004 }) {
  const initial = agent.name ? agent.name[0].toUpperCase() : '#';

  const webEndpoint = agent.services?.web?.endpoint;
  const mcpEndpoint = agent.services?.mcp?.endpoint;
  const chainBadges = registryChainBadges(agent);
  const healthBadge = registryHealthBadge(agent.health_status);
  const capabilityBadges = registryCapabilityBadges(agent);

  return (
    <ItemCard>
      <AgentTopRow>
        <Flex
          css={{
            alignItems: 'center',
            gap: '$xs',
            flexWrap: 'wrap',
            minWidth: 0,
          }}
        >
          <Avatar src={agent.image_url} alt={agent.name} fallback={initial} />
          <AgentName>{agent.name || `Agent #${agent.token_id}`}</AgentName>
          <AgentId>#{agent.token_id}</AgentId>
          {agent.is_verified && <Badge variant="green">verified</Badge>}
          {chainBadges.map(badge => (
            <Badge key={badge.key} variant={badge.variant}>
              {badge.label}
            </Badge>
          ))}
          {healthBadge && (
            <Badge variant={healthBadge.variant}>{healthBadge.label}</Badge>
          )}
        </Flex>
        <OwnerAddr>{truncateAddress(agent.owner_address)}</OwnerAddr>
      </AgentTopRow>

      {agent.description && <Description>{agent.description}</Description>}

      <MetaRow>
        {agent.x402_supported && <Badge variant="green">x402</Badge>}
        {agent.supported_protocols?.map(p => (
          <Badge key={p} variant="purple">
            {p}
          </Badge>
        ))}
        {(agent.supported_trust_models ?? []).map(t => (
          <Badge key={t} variant="yellow">
            {t}
          </Badge>
        ))}
        {agent.total_score > 0 && (
          <Badge variant="code">{agent.total_score} pts</Badge>
        )}
        {agent.star_count > 0 && (
          <Badge variant="code">{agent.star_count} stars</Badge>
        )}
      </MetaRow>

      {capabilityBadges.length > 0 && (
        <CapabilityRow>
          {capabilityBadges.map(badge => (
            <Badge key={badge.key} variant={badge.variant}>
              {badge.label}
            </Badge>
          ))}
        </CapabilityRow>
      )}

      {(webEndpoint || mcpEndpoint) && (
        <Flex css={{ gap: '$sm', marginTop: '$xs', flexWrap: 'wrap' }}>
          {webEndpoint && (
            <EndpointLink
              href={webEndpoint}
              target="_blank"
              rel="noopener noreferrer"
            >
              {webEndpoint}
            </EndpointLink>
          )}
          {mcpEndpoint && (
            <EndpointLink
              href={mcpEndpoint}
              target="_blank"
              rel="noopener noreferrer"
            >
              MCP: {mcpEndpoint}
            </EndpointLink>
          )}
        </Flex>
      )}
    </ItemCard>
  );
}

const PROTOCOL_FILTERS = ['MCP', 'A2A', 'Web'] as const;

export function AgentRegistry() {
  const { search, debouncedSearch, handleSearchChange } = useSearch();
  const {
    x402Only,
    setX402Only,
    protocolFilter,
    toggleProtocol,
    chainFilter,
    setChainFilter,
    hasActiveFilters,
  } = useAgentFilters();
  const {
    agents,
    loading,
    error,
    page,
    totalPages,
    nextPage,
    prevPage,
    total,
    refetch,
  } = useRegistryAgents(
    debouncedSearch || undefined,
    chainFilter,
    protocolFilter,
    x402Only
  );

  const CHAIN_LABELS: Partial<Record<string, string>> = {
    mainnet: 'Ethereum',
    base: 'Base',
    'base-sepolia': 'Base Sepolia',
    sepolia: 'Sepolia',
    hoodi: 'Hoodi',
  };
  const chainLabel = chainFilter
    ? (CHAIN_LABELS[chainFilter] ?? chainFilter)
    : 'Ethereum';

  const headerContent = (
    <Flex
      css={{
        alignItems: 'center',
        gap: '$md',
        flex: 1,
        minWidth: 0,
        paddingRight: '$xl',
      }}
    >
      {!loading && total > 0 && (
        <AvatarStack css={{ flexShrink: 0 }}>
          {SEEDS.map((seed, i) => (
            <AvatarCircle
              key={i}
              style={{
                marginLeft: i === 0 ? 0 : -8,
                position: 'relative',
                zIndex: SEEDS.length - i,
                backgroundColor: seed.bg,
                color: seed.color,
              }}
            >
              {seed.letter}
            </AvatarCircle>
          ))}
        </AvatarStack>
      )}
      <Box css={{ flex: 1, minWidth: 0 }}>
        <Flex
          css={{
            alignItems: 'center',
            gap: '$sm',
            flexWrap: 'wrap',
            width: 'auto',
          }}
        >
          <SectionTitle>ERC-8004 On-chain Agent Registry</SectionTitle>
          {!loading && total > 0 && (
            <Text css={{ fontSize: '$2', color: '$muted' }}>
              {total.toLocaleString()} on {chainLabel}
            </Text>
          )}
        </Flex>
        <Text css={{ fontSize: '$1', color: '$muted', marginTop: 2 }}>
          Single-chain view · marketplace combines all 3 chains (Ethereum + Base
          + Base Sepolia) · data from 8004scan.io
        </Text>
      </Box>
    </Flex>
  );

  return (
    <Card section>
      <Accordion header={headerContent} defaultExpanded={false}>
        {agents.length > 0 && (
          <Flex
            css={{
              flexWrap: 'wrap',
              justifyContent: 'space-between',
              paddingTop: '$lg',
            }}
          >
            <ToolBar>
              <FilterChip
                active={x402Only}
                onClick={() => setX402Only(v => !v)}
              >
                x402
              </FilterChip>
              {PROTOCOL_FILTERS.map(p => (
                <FilterChip
                  key={p}
                  active={protocolFilter === p}
                  onClick={() => toggleProtocol(p)}
                >
                  {p}
                </FilterChip>
              ))}
            </ToolBar>
            <ToolBar>
              <SearchInput
                placeholder="Search agents..."
                value={search}
                onChange={handleSearchChange}
              />
              <ChainSelector
                value={chainFilter}
                onChange={setChainFilter}
                allowDeselect
              />
            </ToolBar>
          </Flex>
        )}
        <AgentList>
          {loading && agents.length === 0 && (
            <LoadingState size="small" message="Querying agent registry..." />
          )}
          {error && (
            <ErrorState
              message={`Registry unavailable: ${error}`}
              onRetry={refetch}
            />
          )}
          {!loading && !error && agents.length === 0 && (
            <EmptyMessage>
              {search || hasActiveFilters
                ? 'No agents match your filters'
                : 'No registered agents found'}
            </EmptyMessage>
          )}
          {agents.map(agent => (
            <AgentCardItem
              key={`${agent.chain_id}-${agent.token_id}`}
              agent={agent}
            />
          ))}
        </AgentList>
        <Pagination
          page={page}
          totalPages={totalPages}
          onPrev={prevPage}
          onNext={nextPage}
          disabled={loading}
        />
      </Accordion>
    </Card>
  );
}
