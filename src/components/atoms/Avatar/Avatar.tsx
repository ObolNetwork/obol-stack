import React, { useState } from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface AvatarProps {
  src?: string | null;
  alt?: string;
  fallback: string;
  size?: number;
}

const AvatarImg = styled('img', {
  width: '$lg',
  height: '$lg',
  borderRadius: '$round',
  objectFit: 'cover',
  backgroundColor: '$bg01',
});

const AvatarFallback = styled(Box, {
  width: '$lg',
  height: '$lg',
  borderRadius: '$round',
  backgroundColor: 'rgba(145, 103, 228, 0.25)',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  fontSize: '$1',
  color: '$coordinate',
  fontWeight: '$semibold',
});

export function Avatar({ src, alt, fallback }: AvatarProps) {
  const [imgError, setImgError] = useState(false);

  if (src && !imgError) {
    return <AvatarImg src={src} alt={alt} onError={() => setImgError(true)} />;
  }

  return <AvatarFallback>{fallback}</AvatarFallback>;
}
