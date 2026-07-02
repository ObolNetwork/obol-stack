'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { CopyButton } from '../atoms/CopyButton';
import { SectionTitle } from '../atoms/SectionTitle';
import { buildAgentCreateWorkflowCli } from '@/lib/sell/cli-commands';

const WorkflowBlock = styled(Box, {
  marginTop: '$xs',
  display: 'flex',
  flexDirection: 'column',
  gap: '$sm',
});

const CommandBlock = styled(Box, {
  padding: '$xs',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
});

const CommandLabel = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  marginBottom: '$xxxs',
});

const CommandLine = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
});

const CommandCode = styled('code', {
  flex: 1,
  fontSize: '12px',
  color: '$light',
  fontFamily: 'monospace',
  wordBreak: 'break-all',
});

const EXAMPLE_AGENT = 'obol-agent';
const WORKFLOW = buildAgentCreateWorkflowCli(EXAMPLE_AGENT);

interface CreateAgentWorkflowProps {
  /** Section title — home page copy uses operator-facing labels. */
  title?: string;
}

const WorkflowHeader = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$sm',
});

// Matches the AccordionContainer inset of the sibling "Sub-agents"
// subsection so both level-2 headers align inside the section card.
const WorkflowContainer = styled(Box, {
  padding: '$lg',
});

const PurposeCopy = styled(Text, {
  fontSize: '$2',
  color: '$muted',
  marginTop: '$xxs',
});

export function CreateAgentWorkflow({
  title = 'Create an agent',
}: CreateAgentWorkflowProps) {
  return (
    <WorkflowContainer>
      <WorkflowHeader>
        <SectionTitle level={2}>{title}</SectionTitle>
        <CopyButton text={WORKFLOW.copyText} title="Copy both commands" />
      </WorkflowHeader>
      <PurposeCopy>Create a new agent to sell its services.</PurposeCopy>
      <WorkflowBlock>
        {WORKFLOW.steps.map(step => (
          <CommandBlock key={step.label}>
            <CommandLabel>{step.label}</CommandLabel>
            <CommandLine>
              <CommandCode>{step.command}</CommandCode>
              <CopyButton text={step.command} title={`Copy ${step.command}`} />
            </CommandLine>
          </CommandBlock>
        ))}
      </WorkflowBlock>
    </WorkflowContainer>
  );
}
