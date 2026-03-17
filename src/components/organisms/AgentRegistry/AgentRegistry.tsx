'use client';

import { Box, Flex, Text, Link, styled } from '@obolnetwork/obol-ui';
import { Accordion } from '../../molecules/Accordion';
import { useRegistryAgents } from '../../../hooks/useRegistryAgents';
import type { Agent8004 } from '@/types/registry';
import { useState, useMemo } from 'react';

const Container = styled(Box, {
  borderRadius: '$4',
  backgroundColor: '$bg02',
  border: '1px solid $stroke',
  overflow: 'hidden',
});

const CountBadge = styled(Box, {
  backgroundColor: '#2FE4AB20',
  color: '#2FE4AB',
  borderRadius: '12px',
  padding: '2px 10px',
  fontSize: '$1',
  fontWeight: '$semibold',
});

const ChainBadge = styled(Box, {
  backgroundColor: '$bg03',
  color: '$body',
  borderRadius: '$pill',
  padding: '2px 10px',
  fontSize: '$1',
  fontWeight: '$medium',
});

const AgentList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  padding: '$sm $lg $md',
});

const FilterBar = styled(Flex, {
  gap: '6px',
  flexWrap: 'wrap',
  padding: '0 $lg $xs',
});

const FilterChip = styled('button', {
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$pill',
  color: '$body',
  fontSize: '11px',
  fontWeight: '$medium',
  padding: '3px 10px',
  cursor: 'pointer',
  transition: 'all 0.15s ease',
  '&:hover': { borderColor: '$body' },

  variants: {
    active: {
      true: {
        backgroundColor: '#2FE4AB20',
        borderColor: '#2FE4AB',
        color: '#2FE4AB',
      },
    },
  },
});

const SearchInput = styled('input', {
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
  color: '$light',
  fontSize: '$2',
  padding: '6px 12px',
  outline: 'none',
  width: '200px',
  '&::placeholder': { color: '$muted' },
  '&:focus': { borderColor: '#2FE4AB' },
});

const AgentCard = styled(Box, {
  padding: '$sm $md',
  borderRadius: '$2',
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  transition: 'border-color 0.2s ease',
  '&:hover': { borderColor: '$body' },
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
  gap: '6px',
  flexWrap: 'wrap',
  marginTop: '$xxs',
});

const Tag = styled(Box, {
  padding: '1px 8px',
  borderRadius: '$pill',
  fontSize: '11px',
  fontWeight: '$medium',

  variants: {
    variant: {
      x402: {
        backgroundColor: '#2FE4AB15',
        color: '#2FE4AB',
      },
      protocol: {
        backgroundColor: '#6366F115',
        color: '#818CF8',
      },
      trust: {
        backgroundColor: '#F59E0B15',
        color: '#FBBF24',
      },
      score: {
        backgroundColor: '$bg01',
        color: '$body',
        border: '1px solid $stroke',
        fontFamily: 'monospace',
      },
      verified: {
        backgroundColor: '#2FE4AB15',
        color: '#2FE4AB',
      },
    },
  },
});

const Avatar = styled('img', {
  width: 24,
  height: 24,
  borderRadius: '50%',
  objectFit: 'cover',
  backgroundColor: '$bg01',
});

const AvatarFallback = styled(Box, {
  width: 24,
  height: 24,
  borderRadius: '50%',
  backgroundColor: '#2FE4AB20',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  fontSize: '11px',
  color: '#2FE4AB',
  fontWeight: '$semibold',
});

const OwnerAddr = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  fontFamily: 'monospace',
});

const EndpointLink = styled(Link, {
  fontSize: '$1',
  color: '$body',
  fontFamily: 'monospace',
  textDecoration: 'none',
  '&:hover': { color: '#2FE4AB' },
});

const EmptyMessage = styled(Text, {
  color: '$body',
  fontSize: '$3',
  textAlign: 'center',
  padding: '$lg 0',
});

function truncateAddr(addr: string): string {
  if (!addr || addr.length < 10) return addr;
  return `${addr.slice(0, 6)}...${addr.slice(-4)}`;
}

function AgentCardItem({ agent }: { agent: Agent8004 }) {
  const [imgError, setImgError] = useState(false);
  const initial = agent.name ? agent.name[0].toUpperCase() : '#';

  const webEndpoint = agent.services?.web?.endpoint;
  const mcpEndpoint = agent.services?.mcp?.endpoint;

  return (
    <AgentCard>
      <AgentTopRow>
        <Flex css={{ alignItems: 'center', gap: '$xs' }}>
          {agent.image_url && !imgError ? (
            <Avatar
              src={agent.image_url}
              alt={agent.name}
              onError={() => setImgError(true)}
            />
          ) : (
            <AvatarFallback>{initial}</AvatarFallback>
          )}
          <AgentName>{agent.name || `Agent #${agent.token_id}`}</AgentName>
          <AgentId>#{agent.token_id}</AgentId>
          {agent.is_verified && <Tag variant="verified">verified</Tag>}
        </Flex>
        <OwnerAddr>{truncateAddr(agent.owner_address)}</OwnerAddr>
      </AgentTopRow>

      {agent.description && <Description>{agent.description}</Description>}

      <MetaRow>
        {agent.x402_supported && <Tag variant="x402">x402</Tag>}
        {agent.supported_protocols?.map(p => (
          <Tag key={p} variant="protocol">
            {p}
          </Tag>
        ))}
        {agent.supported_trust_models?.map(t => (
          <Tag key={t} variant="trust">
            {t}
          </Tag>
        ))}
        {agent.total_score > 0 && (
          <Tag variant="score">{agent.total_score} pts</Tag>
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
    </AgentCard>
  );
}

const PROTOCOL_FILTERS = ['MCP', 'A2A', 'Web'] as const;

export function AgentRegistry() {
  const [search, setSearch] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [x402Only, setX402Only] = useState(false);
  const [protocolFilter, setProtocolFilter] = useState<string | undefined>();

  const { agents, loading, error } = useRegistryAgents(
    10,
    debouncedSearch || undefined,
    protocolFilter
  );

  // Client-side x402 filter (8004scan doesn't support server-side)
  const filtered = useMemo(
    () => (x402Only ? agents.filter(a => a.x402_supported) : agents),
    [agents, x402Only]
  );

  const handleSearchChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    setSearch(value);
    clearTimeout((handleSearchChange as any)._timer);
    (handleSearchChange as any)._timer = setTimeout(
      () => setDebouncedSearch(value),
      400
    );
  };

  const toggleProtocol = (p: string) => {
    setProtocolFilter(prev => (prev === p ? undefined : p));
  };

  const headerContent = (
    <Flex
      css={{
        alignItems: 'center',
        justifyContent: 'space-between',
        width: '100%',
      }}
    >
      <Flex css={{ alignItems: 'center', gap: '$sm' }}>
        <Text
          css={{ fontSize: '$5', fontWeight: '$semibold', color: '$light' }}
        >
          Agent Registry
        </Text>
        {!loading && filtered.length > 0 && (
          <CountBadge>{filtered.length}</CountBadge>
        )}
      </Flex>
      <Flex css={{ alignItems: 'center', gap: '$sm' }}>
        {agents.length > 0 && (
          <SearchInput
            placeholder="Search agents..."
            value={search}
            onChange={handleSearchChange}
            onClick={(e: React.MouseEvent) => e.stopPropagation()}
          />
        )}
        <ChainBadge>Base</ChainBadge>
      </Flex>
    </Flex>
  );

  return (
    <Container>
      <Accordion header={headerContent} defaultExpanded={false}>
        <FilterBar>
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
        </FilterBar>
        <AgentList>
          {loading && <EmptyMessage>Querying agent registry...</EmptyMessage>}
          {error && <EmptyMessage>Registry unavailable: {error}</EmptyMessage>}
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
    </Container>
  );
}
