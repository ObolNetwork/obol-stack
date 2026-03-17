import { Flex, Text, styled } from '@obolnetwork/obol-ui';

export interface PaginationProps {
  page: number;
  totalPages: number;
  onPrev: () => void;
  onNext: () => void;
  disabled?: boolean;
}

const PageButton = styled('button', {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  width: 28,
  height: 28,
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
  color: '$body',
  fontSize: '$1',
  cursor: 'pointer',
  transition: 'all 0.15s ease',

  '&:hover:not(:disabled)': {
    background: '$bg04',
    color: '$light',
    borderColor: '$bg05',
  },

  '&:disabled': {
    opacity: 0.3,
    cursor: 'not-allowed',
  },
});

export function Pagination({
  page,
  totalPages,
  onPrev,
  onNext,
  disabled,
}: PaginationProps) {
  if (totalPages <= 1) return null;

  return (
    <Flex
      css={{
        alignItems: 'center',
        gap: '$xs',
        justifyContent: 'center',
        paddingTop: '$sm',
      }}
    >
      <PageButton onClick={onPrev} disabled={disabled || page <= 1}>
        &#8592;
      </PageButton>
      <Text
        css={{
          fontSize: '$1',
          color: '$muted',
          minWidth: 60,
          textAlign: 'center',
        }}
      >
        {page} / {totalPages}
      </Text>
      <PageButton onClick={onNext} disabled={disabled || page >= totalPages}>
        &#8594;
      </PageButton>
    </Flex>
  );
}
