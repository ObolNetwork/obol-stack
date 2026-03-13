'use client';

import { Box, Flex, Text, Link, styled } from '@obolnetwork/obol-ui';
import { Accordion } from '../../molecules/Accordion';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { Badge } from '../../atoms/Badge';
import { Card } from '../../atoms/Card';
import { ChainChip } from '../../atoms/ChainChip';
import { CountBadge } from '../../atoms/CountBadge';
import { SectionTitle } from '../../atoms/SectionTitle';
import { ItemCard } from '../../atoms/ItemCard';
import { SearchInput } from '../../atoms/SearchInput';
import { FilterChip } from '../../atoms/FilterChip';
import { Avatar } from '../../atoms/Avatar';
import { EmptyMessage } from '../../atoms/EmptyMessage';
import { useRegistryAgents } from '../../../hooks/useRegistryAgents';
import { useSearch } from '../../../hooks/useSearch';
import { truncateAddress } from '../../../utils/address';
import type { Agent8004 } from '@/types/registry';
import { useState, useMemo } from 'react';

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
  alignItems: 'center',
  justifyContent: 'space-between',
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

  return (
    <ItemCard>
      <AgentTopRow>
        <Flex css={{ alignItems: 'center', gap: '$xs' }}>
          <Avatar src={agent.image_url} alt={agent.name} fallback={initial} />
          <AgentName>{agent.name || `Agent #${agent.token_id}`}</AgentName>
          <AgentId>#{agent.token_id}</AgentId>
          {agent.is_verified && <Badge variant="green">verified</Badge>}
        </Flex>
        <OwnerAddr>{truncateAddress(agent.owner_address)}</OwnerAddr>
      </AgentTopRow>

      {agent.description && <Description>{agent.description}</Description>}

      <MetaRow>
        {agent.x402_supported && <Badge variant="green">x402</Badge>}
        {agent.supported_protocols.map(p => (
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
      </MetaRow>

      {(webEndpoint || mcpEndpoint) && (
        <Flex css={{ gap: '$sm', marginTop: '$xs', flexWrap: 'wrap' }}>
          {webEndpoint && (
            <EndpointLink href={webEndpoint} target="_blank">
              {webEndpoint}
            </EndpointLink>
          )}
          {mcpEndpoint && (
            <EndpointLink href={mcpEndpoint} target="_blank">
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
  const [x402Only, setX402Only] = useState(false);
  const [protocolFilter, setProtocolFilter] = useState<string | undefined>();

  const { agents, loading, error } = useRegistryAgents(
    20,
    debouncedSearch || undefined,
    protocolFilter
  );

  // Client-side x402 filter (8004scan doesn't support server-side)
  const filtered = useMemo(
    () => (x402Only ? agents.filter(a => a.x402_supported) : agents),
    [agents, x402Only]
  );

  const toggleProtocol = (p: string) => {
    setProtocolFilter(prev => (prev === p ? undefined : p));
  };

  const headerContent = (
    <Flex css={{ alignItems: 'center', gap: '$sm' }}>
      <SectionTitle>Agent Registry</SectionTitle>
      {!loading && filtered.length > 0 && (
        <CountBadge count={filtered.length} />
      )}
    </Flex>
  );

  return (
    <Card section>
      <Accordion header={headerContent} defaultExpanded={true}>
        <Flex
          css={{
            flexWrap: 'wrap',
            justifyContent: 'space-between',
            paddingTop: '$lg',
          }}
        >
          <ToolBar>
            <FilterChip active={x402Only} onClick={() => setX402Only(v => !v)}>
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
            <ChainChip variant="sepolia" />
          </ToolBar>
        </Flex>
        <AgentList>
          {loading && (
            <LoadingState size="small" message="Querying agent registry..." />
          )}
          {error && <ErrorState message={`Registry unavailable: ${error}`} />}
          {!loading && !error && filtered.length === 0 && (
            <EmptyMessage>
              {search || x402Only || protocolFilter
                ? 'No agents match your filters'
                : 'No registered agents found'}
            </EmptyMessage>
          )}
          {filtered.map(agent => (
            <AgentCardItem key={agent.token_id} agent={agent} />
          ))}
        </AgentList>
      </Accordion>
    </Card>
  );
}
