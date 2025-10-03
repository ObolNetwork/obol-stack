import { CopilotSidebar, useChatContext } from '@copilotkit/react-ui';
import { Box, Image, styled } from '@obolnetwork/obol-ui';
import { Icon } from '../../atoms/Icon';
import { StatusIndicator } from './StatusIndicator';
import { determineNodeStatus } from './statusLogic';
import { DashboardData } from '../../../types/dashboard';
import { useCoAgent } from '@copilotkit/react-core';

// Styled Components
const HeaderContainer = styled(Box, {
  display: 'flex',
  width: '100%',
  height: '64px',
  padding: '8px',
  justifyContent: 'space-between',
  alignItems: 'center',
  borderBottom: '1px solid var(--Backgrounds-05, #2D4D53)',
});

const AIImage = styled(Image, {
  width: '48px',
  height: '48px',
  objectFit: 'contain',
});

const StatusContainer = styled(Box, {
  width: 'fit-content',
  display: 'flex',
  justifyContent: 'center',
});

const CloseButton = styled(Box, {
  width: '24px',
  height: '24px',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  cursor: 'pointer',
  color: 'var(--Text-Secondary, #b8c5c7)',
  transition: 'color 0.2s ease',

  '&:hover': {
    color: '#ffffff',
  },
});

const ButtonContainer = styled(Box, {
  position: 'sticky',
  top: '136px',
  right: '64px',
  width: '80px',
  height: '80px',
  aspectRatio: '1/1',
  flexShrink: 0,
  background: 'none',
  border: 'none',
  cursor: 'pointer',
  padding: 0,
});

const ButtonImage = styled(Image, {
  width: '100%',
  height: '100%',
  objectFit: 'contain',
});

interface HeaderProps {
  data: DashboardData;
}

const Header = ({ data }: HeaderProps) => {
  const status = determineNodeStatus(data);
  const { setOpen } = useChatContext();

  const handleClose = () => {
    setOpen(false);
  };

  return (
    <HeaderContainer>
      {/* Left: AI Image */}
      <AIImage src="/ai.png" alt="AI Assistant" width={48} height={48} />

      {/* Middle: Status Indicator */}
      <StatusContainer>
        <StatusIndicator status={status} />
      </StatusContainer>

      {/* Right: Close Icon */}
      <CloseButton onClick={handleClose}>
        <Icon name="close" size="s" />
      </CloseButton>
    </HeaderContainer>
  );
};

const Button = () => {
  const { open, setOpen } = useChatContext();

  const handleClick = () => {
    setOpen(!open);
  };

  return (
    <ButtonContainer onClick={handleClick}>
      <ButtonImage src="/ai.png" alt="AI Assistant" width={80} height={80} />
    </ButtonContainer>
  );
};

interface AgentSidebarProps {
  onSidebarToggle: (isOpen: boolean) => void;
  data: DashboardData;
}

export function AgentSidebar({ onSidebarToggle, data }: AgentSidebarProps) {
  useCoAgent({
    name: 'obol_agent',
  });
  return (
    <CopilotSidebar
      Header={() => <Header data={data} />}
      Button={Button}
      defaultOpen={false}
      clickOutsideToClose={true}
      onSetOpen={onSidebarToggle}
      labels={{
        title: 'Obol Assistant',
        initial:
          "👋 Hi! I'm your Obol ADK Agent.\n\nI can help you with:\n- Monitoring your Obol Stack\n- Understanding node metrics\n- Troubleshooting issues\n- Explaining system status\n\nHow can I assist you today?",
      }}
    />
  );
}
