import { Flex, Text } from '@obolnetwork/obol-ui';
import { Button } from '../Button/Button';

export interface PaginationProps {
  page: number;
  totalPages: number;
  onPrev: () => void;
  onNext: () => void;
  disabled?: boolean;
}

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
      <Button
        type="button"
        variant="ghost"
        size="sm"
        iconOnly
        aria-label="Previous page"
        onClick={onPrev}
        disabled={disabled || page <= 1}
      >
        &#8592;
      </Button>
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
      <Button
        type="button"
        variant="ghost"
        size="sm"
        iconOnly
        aria-label="Next page"
        onClick={onNext}
        disabled={disabled || page >= totalPages}
      >
        &#8594;
      </Button>
    </Flex>
  );
}
