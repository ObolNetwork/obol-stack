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

const NotPublished = styled(Text, {
  fontSize: '$2',
  color: '$body',
  padding: '$sm 0',
});

export function AgentRegistrationCard() {
  const { registration, tunnelURL, loading } = useAgentRegistration();

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

              {registration.services?.length > 0 && (
                <Box>
                  {registration.services
                    .filter((s: AgentRegistrationService) => s.endpoint)
                    .map((s: AgentRegistrationService) => (
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
