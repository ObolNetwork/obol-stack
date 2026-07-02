'use client';

import { Box, Flex, Text, Link, styled } from '@obolnetwork/obol-ui';
import { OpenInNew } from '@obolnetwork/obol-ui';
import { tones } from '../../../lib/ui/tones';
import { Card } from '../../atoms/Card';
import { Badge } from '../../atoms/Badge';
import { Avatar } from '../../atoms/Avatar';
import { Accordion } from '../../molecules/Accordion';
import { SectionTitle } from '../../atoms/SectionTitle';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { useAgentRegistration } from '../../../hooks/useAgents';
import {
  agentRegistrationExplorerUrl,
  agentRegistrationLabel,
} from '@/lib/services/agent-registration';
import {
  AGENT_REGISTRATION_PATH,
  publicResourceUrl,
} from '@/lib/services/public-storefront';
import { listPublishedRegistrationServices } from '@/lib/services/registration-published-services';

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
  backgroundColor: tones.neutral.bg,
  border: `1px solid ${tones.neutral.border}`,
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

const ServiceGrid = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const ServiceCard = styled(Box, {
  border: '1px solid $stroke',
  borderRadius: '$2',
  background: '$bg03',
  padding: '$sm',
  display: 'flex',
  flexDirection: 'column',
  gap: '$xxs',
});

const ServiceName = styled(Text, {
  fontSize: '$3',
  fontWeight: '$semibold',
  color: '$light',
});

const IdentitySummary = styled(Text, {
  fontSize: '$2',
  color: '$body',
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
  const { registration, tunnelURL, loading, error, refetch } =
    useAgentRegistration();
  const registrationEntries = registration?.registrations ?? [];
  const topLevelMetadata = metadataEntries(registration?.metadata);
  const provenanceEntries = metadataEntries(registration?.provenance);
  const publishedServices = listPublishedRegistrationServices(
    registration?.services ?? []
  );
  const rawRegistrationUrl = publicResourceUrl(
    tunnelURL,
    AGENT_REGISTRATION_PATH
  );

  const headerContent = (
    <Box>
      <Flex css={{ alignItems: 'center', gap: '$sm', flexWrap: 'wrap' }}>
        <SectionTitle>Registrations</SectionTitle>
        {!loading && publishedServices.length > 0 && (
          <Badge variant="neutral">{publishedServices.length} services</Badge>
        )}
      </Flex>
      <Text css={{ fontSize: '$1', color: '$muted', marginTop: 2 }}>
        Where this stack&apos;s agents are advertised for discovery.
      </Text>
    </Box>
  );

  return (
    <Card section>
      <Accordion header={headerContent} defaultExpanded={true}>
        <Body>
          {loading && (
            <LoadingState size="small" message="Loading registrations..." />
          )}

          {!loading && error && (
            <ErrorState
              message={`Registrations unavailable: ${error}`}
              onRetry={refetch}
            />
          )}

          {!loading && !error && publishedServices.length === 0 && (
            <NotPublished>
              No registrations yet. Run <code>obol sell register</code> to list
              your agents on the Ethereum Agent Registry.
            </NotPublished>
          )}

          {!loading &&
            !error &&
            registration &&
            publishedServices.length > 0 && (
              <>
                <TopRow>
                  <Avatar
                    src={registration.image}
                    alt={registration.name}
                    fallback={registration.name?.[0]?.toUpperCase() ?? 'A'}
                  />
                  <Box>
                    <AgentName>{registration.name}</AgentName>
                    <IdentitySummary>
                      On-chain registration document —{' '}
                      {publishedServices.length} service
                      {publishedServices.length === 1 ? '' : 's'} published
                      below.
                    </IdentitySummary>
                  </Box>
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

                {publishedServices.length > 0 && (
                  <DetailBlock>
                    <DetailTitle>Published services</DetailTitle>
                    <ServiceGrid>
                      {publishedServices.map(service => (
                        <ServiceCard key={service.endpoint}>
                          <ServiceName>{service.name}</ServiceName>
                          {service.skills.length > 0 && (
                            <ChipRow>
                              {service.skills.map(skill => (
                                <MetaChip key={`${service.name}-${skill}`}>
                                  {skill}
                                </MetaChip>
                              ))}
                            </ChipRow>
                          )}
                          <EndpointLink
                            href={service.endpoint}
                            target="_blank"
                            rel="noopener noreferrer"
                          >
                            {service.endpoint}
                            <OpenInNew size="sm" />
                          </EndpointLink>
                        </ServiceCard>
                      ))}
                    </ServiceGrid>
                  </DetailBlock>
                )}

                {registrationEntries.length > 0 && (
                  <DetailBlock>
                    <DetailTitle>On-chain registrations</DetailTitle>
                    {registrationEntries.map((entry, index) => {
                      const label = agentRegistrationLabel(entry);
                      const explorerUrl = agentRegistrationExplorerUrl(entry);
                      return (
                        <IdentityRow
                          key={`${entry.agentRegistry}-${entry.agentId}-${index}`}
                        >
                          {explorerUrl ? (
                            <IdentityLink
                              href={explorerUrl}
                              target="_blank"
                              rel="noopener noreferrer"
                            >
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

                <Flex css={{ marginTop: '$xs' }}>
                  <Link
                    href={rawRegistrationUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                    css={{ fontSize: '$1' }}
                  >
                    View raw registration JSON
                    <OpenInNew size="sm" />
                  </Link>
                </Flex>
              </>
            )}
        </Body>
      </Accordion>
    </Card>
  );
}
