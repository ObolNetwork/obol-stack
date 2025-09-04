import * as AccordionPrimitive from '@radix-ui/react-accordion';
import { styled } from '@obolnetwork/obol-ui';

const StyledItem = styled(AccordionPrimitive.Item, {});

const StyledRoot = styled(AccordionPrimitive.Root, {
  background: '$bg02',
  borderRadius: '$4',
  color: '$body',
  width: '$full',
  padding: '$sm',
});

const StyledTrigger = styled(AccordionPrimitive.Trigger, {
  width: '100%',
  textAlign: 'left',
  background: 'none',
  border: 'none',
  color: '$body',
  fontWeight: '$medium',
  fontSize: '$3',
  padding: 0,
  cursor: 'pointer',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$sm',
  '& .AccordionChevron': {
    color: '$light',
    transition: 'transform 300ms cubic-bezier(0.87, 0, 0.13, 1)',
  },
  "&[data-state='open'] > .AccordionChevron": {
    transform: 'rotate(180deg)',
  },
});

const StyledHeader = styled(AccordionPrimitive.Header, {
  padding: 0,
  margin: 0,
});

const StyledContent = styled(AccordionPrimitive.Content, {
  marginTop: '$xl',
  color: '$body',
});

export const Accordion = {
  Root: StyledRoot,
  Trigger: StyledTrigger,
  Header: StyledHeader,
  Content: StyledContent,
  Item: StyledItem,
};
