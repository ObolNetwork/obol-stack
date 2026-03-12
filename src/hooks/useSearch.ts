import { useState, useCallback, useRef } from 'react';

export interface UseSearchReturn {
  search: string;
  debouncedSearch: string;
  handleSearchChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  clearSearch: () => void;
}

export function useSearch(delay = 400): UseSearchReturn {
  const [search, setSearch] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const handleSearchChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const value = e.target.value;
      setSearch(value);
      if (timerRef.current) clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => setDebouncedSearch(value), delay);
    },
    [delay]
  );

  const clearSearch = useCallback(() => {
    if (timerRef.current) clearTimeout(timerRef.current);
    setSearch('');
    setDebouncedSearch('');
  }, []);

  return { search, debouncedSearch, handleSearchChange, clearSearch };
}
