'use client';

import { Box, styled } from '@obolnetwork/obol-ui';
import { useEffect, useState } from 'react';

const Banner = styled(Box, {
  position: 'sticky',
  top: 0,
  zIndex: 50,
  padding: '$sm $md',
  backgroundColor: 'rgba(255, 193, 7, 0.12)',
  borderBottom: '1px solid rgba(255, 193, 7, 0.35)',
  color: '$light',
  fontSize: '$2',
  lineHeight: 1.5,
  textAlign: 'center',

  '& code': {
    fontFamily: 'var(--font-dm-sans), monospace',
    fontSize: '$2',
  },
});

/**
 * Shown during local `npm run dev` when opened via localhost instead of obol.stack.
 */
export function LocalDevHostBanner() {
  const [show, setShow] = useState(false);

  useEffect(() => {
    const host = window.location.hostname;
    setShow(host === 'localhost' || host === '127.0.0.1');
  }, []);

  if (!show) {
    return null;
  }

  return (
    <Banner role="status">
      Local dev works best at <code>http://obol.stack:3000</code> (not
      localhost). Restart with <code>npm run dev</code> and ensure{' '}
      <code>obol.stack</code> resolves to 127.0.0.1 in <code>/etc/hosts</code>.
    </Banner>
  );
}
