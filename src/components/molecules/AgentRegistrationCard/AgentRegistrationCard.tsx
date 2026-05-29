'use client';

import { Box, Flex, Text, Link, styled } from '@obolnetwork/obol-ui';
import { OpenInNew } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Badge } from '../../atoms/Badge';
import { Avatar } from '../../atoms/Avatar';
import { Accordion } from '../../molecules/Accordion';
import { SectionTitle } from '../../atoms/SectionTitle';
import { LoadingState } from '../../molecules/LoadingState';
import {
  useAgentRegistration,
  type AgentRegistrationService,
} from '../../../hooks/useAgents';
import {
  agentRegistrationExplorerUrl,
  agentRegistrationLabel,
} from '@/lib/services/agent-registration';
import {
  hasCapabilityTaxonomy,
  serviceAvailabilityLabel,
} from '@/lib/services/agent-registration-capabilities';

const Body = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$sm',
  paddingTop: '$lg',
});

const TopRow = styled(Flex, {
  alignItems: 'center',
  gap: '$sm',
  flexWrap: 'wrap',
});

const AgentName = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
});

const Description = styled(Text, {
  fontSize: '$2',
  color: '$body',
  lineHeight: 1.5,
});

const ServiceRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  flexWrap: 'wrap',
});

const ServiceLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
  minWidth: '36px',
});

const EndpointLink = styled(Link, {
  fontSize: '$1',
  fontFamily: 'monospace',
  wordBreak: 'break-all',
});

const MetaRow = styled(Flex, {
  gap: '$xs',
  flexWrap: 'wrap',
  alignItems: 'center',
});

const DetailBlock = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xxs',
  padding: '$xs 0',
});

const DetailTitle = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  fontWeight: '$medium',
  textTransform: 'uppercase',
});

const IdentityRow = styled(Flex, {
  flexDirection: 'column',
  gap: '2px',
});

const IdentityText = styled(Text, {
  fontSize: '$1',
  color: '$light',
  fontFamily: 'monospace',
  wordBreak: 'break-all',
});

const IdentityLink = styled(Link, {
  fontSize: '$1',
  color: '$obolGreen',
  fontFamily: 'monospace',
  wordBreak: 'break-all',
});

const RegistryText = styled(Text, {
  fontSize: '10px',
  color: '$muted',
  fontFamily: 'monospace',
  wordBreak: 'break-all',
});

const ChipRow = styled(Flex, {
  gap: '$xxs',
  flexWrap: 'wrap',
});

const MetaChip = styled(Text, {
  fontSize: '10px',
  color: '$body',
  backgroundColor: 'rgba(255, 255, 255, 0.04)',
  border: '1px solid rgba(255, 255, 255, 0.08)',
  borderRadius: '4px',
  padding: '1px 6px',
  lineHeight: 1.4,
  fontFamily: 'monospace',
});

const NotPublished = styled(Text, {
  fontSize: '$2',
  color: '$body',
  padding: '$sm 0',
});

function metadataEntries(
  metadata: Record<string, string> | undefined
): Array<[string, string]> {
  return Object.entries(metadata ?? {}).filter(
    ([key, value]) =>
      key.trim() !== '' && typeof value === 'string' && value.trim() !== ''
  );
}

export function AgentRegistrationCard() {
  const { registration, tunnelURL, loading } = useAgentRegistration();
  const registrationEntries = registration?.registrations ?? [];
  const topLevelMetadata = metadataEntries(registration?.metadata);
  const provenanceEntries = metadataEntries(registration?.provenance);
  const capabilityServices =
    registration?.services?.filter(hasCapabilityTaxonomy) ?? [];
  const endpointServices =
    registration?.services?.filter(
      (s: AgentRegistrationService) => s.endpoint
    ) ?? [];

  const headerContent = <SectionTitle>Agent Registration</SectionTitle>;

  return (
    <Card section>
      <Accordion header={headerContent} defaultExpanded={true}>
        <Body>
          {loading && (
            <LoadingState size="small" message="Loading registration..." />
          )}

          {!loading && !registration && (
            <NotPublished>
              No registration published yet. Run <code>obol sell http …</code>{' '}
              with <code>--registration.enabled=true</code> to publish one.
            </NotPublished>
          )}

          {!loading && registration && (
            <>
              <TopRow>
                <Avatar
                  src={registration.image}
                  alt={registration.name}
                  fallback={registration.name?.[0]?.toUpperCase() ?? 'A'}
                />
                <AgentName>{registration.name}</AgentName>
                <MetaRow>
                  {registration.x402Support && (
                    <Badge variant="green">x402</Badge>
                  )}
                  {registration.active ? (
                    <Badge variant="green">active</Badge>
                  ) : (
                    <Badge variant="neutral">inactive</Badge>
                  )}
                  {(registration.supportedTrust ?? []).map(t => (
                    <Badge key={t} variant="yellow">
                      {t}
                    </Badge>
                  ))}
                </MetaRow>
              </TopRow>

              {registration.description && (
                <Description>{registration.description}</Description>
              )}

              {registrationEntries.length > 0 && (
                <DetailBlock>
                  <DetailTitle>On-chain identity</DetailTitle>
                  {registrationEntries.map((entry, index) => {
                    const label = agentRegistrationLabel(entry);
                    const explorerUrl = agentRegistrationExplorerUrl(entry);
                    return (
                      <IdentityRow
                        key={`${entry.agentRegistry}-${entry.agentId}-${index}`}
                      >
                        {explorerUrl ? (
                          <IdentityLink href={explorerUrl} target="_blank">
                            {label}
                            <OpenInNew size="sm" />
                          </IdentityLink>
                        ) : (
                          <IdentityText>{label}</IdentityText>
                        )}
                        <RegistryText>{entry.agentRegistry}</RegistryText>
                      </IdentityRow>
                    );
                  })}
                </DetailBlock>
              )}

              {topLevelMetadata.length > 0 && (
                <DetailBlock>
                  <DetailTitle>Metadata</DetailTitle>
                  <ChipRow>
                    {topLevelMetadata.map(([key, value]) => (
                      <MetaChip key={key}>
                        {key}={value}
                      </MetaChip>
                    ))}
                  </ChipRow>
                </DetailBlock>
              )}

              {provenanceEntries.length > 0 && (
                <DetailBlock>
                  <DetailTitle>Provenance</DetailTitle>
                  <ChipRow>
                    {provenanceEntries.map(([key, value]) => (
                      <MetaChip key={key}>
                        {key}={value}
                      </MetaChip>
                    ))}
                  </ChipRow>
                </DetailBlock>
              )}

              {capabilityServices.length > 0 && (
                <DetailBlock>
                  <Flex
                    css={{ alignItems: 'center', gap: '$xs', flexWrap: 'wrap' }}
                  >
                    <DetailTitle>Capabilities</DetailTitle>
                    {capabilityServices.map((service, index) => {
                      const label = serviceAvailabilityLabel(service);
                      return label ? (
                        <Badge
                          key={`${service.name}-${service.version ?? index}`}
                          variant="yellow"
                        >
                          {label}
                        </Badge>
                      ) : null;
                    })}
                  </Flex>
                  {capabilityServices.map((service, index) => (
                    <Box key={`${service.name}-${service.version ?? index}`}>
                      {service.version && (
                        <Text css={{ fontSize: '$1', color: '$muted' }}>
                          OASF v{service.version}
                        </Text>
                      )}
                      {(service.skills?.length ?? 0) > 0 && (
                        <ChipRow css={{ marginTop: '$xxs' }}>
                          {service.skills!.map(skill => (
                            <MetaChip key={`skill-${skill}`}>
                              skill:{skill}
                            </MetaChip>
                          ))}
                        </ChipRow>
                      )}
                      {(service.domains?.length ?? 0) > 0 && (
                        <ChipRow css={{ marginTop: '$xxs' }}>
                          {service.domains!.map(domain => (
                            <MetaChip key={`domain-${domain}`}>
                              domain:{domain}
                            </MetaChip>
                          ))}
                        </ChipRow>
                      )}
                    </Box>
                  ))}
                </DetailBlock>
              )}

              {endpointServices.length > 0 && (
                <Box>
                  {endpointServices.map((s: AgentRegistrationService) => (
                    <ServiceRow key={s.name} css={{ marginTop: '$xxs' }}>
                      <ServiceLabel>{s.name}</ServiceLabel>
                      <EndpointLink href={s.endpoint!} target="_blank">
                        {s.endpoint}
                        <OpenInNew size="sm" />
                      </EndpointLink>
                      {s.version && (
                        <Text css={{ fontSize: '$1', color: '$muted' }}>
                          v{s.version}
                        </Text>
                      )}
                    </ServiceRow>
                  ))}
                </Box>
              )}

              {tunnelURL && (
                <Flex css={{ marginTop: '$xs' }}>
                  <Link
                    href={`${tunnelURL.replace(/\/$/, '')}/.well-known/agent-registration.json`}
                    target="_blank"
                    css={{ fontSize: '$1' }}
                  >
                    View raw registration JSON
                    <OpenInNew size="sm" />
                  </Link>
                </Flex>
              )}
            </>
          )}
        </Body>
      </Accordion>
    </Card>
  );
}
