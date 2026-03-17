import React from 'react';
import { ChainName } from '../../../types/dashboard';
import { Text, styled } from '@obolnetwork/obol-ui';

export const CardAppType = styled(Text, {
  fontSize: '$2',
  fontWeight: '$medium',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
});

export const CardAppId = styled(Text, {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
  fontFamily: 'monospace',
  lineHeight: '$tight',
});

const EthereumIcon = ({ size = 32 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 32 32"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
  >
    <path
      d="M16 32C24.8366 32 32 24.8366 32 16C32 7.16344 24.8366 0 16 0C7.16344 0 0 7.16344 0 16C0 24.8366 7.16344 32 16 32Z"
      fill="#627EEA"
    />
    <path
      d="M16.498 4V12.87L23.995 16.22L16.498 4Z"
      fill="white"
      fillOpacity="0.602"
    />
    <path d="M16.498 4L9 16.22L16.498 12.87V4Z" fill="white" />
    <path
      d="M16.498 21.9682V27.9952L24 17.6162L16.498 21.9682Z"
      fill="white"
      fillOpacity="0.602"
    />
    <path d="M16.498 27.9952V21.9672L9 17.6162L16.498 27.9952Z" fill="white" />
    <path
      d="M16.498 20.5731L23.995 16.2201L16.498 12.8721V20.5731Z"
      fill="white"
      fillOpacity="0.2"
    />
    <path
      d="M9 16.2201L16.498 20.5731V12.8721L9 16.2201Z"
      fill="white"
      fillOpacity="0.602"
    />
  </svg>
);

const AztecIcon = ({ size = 32 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 32 32"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
  >
    <g clipPath="url(#clip0_25576_7853)">
      <path
        d="M32 16C32 7.16344 24.8366 0 16 0C7.16344 0 0 7.16344 0 16C0 24.8366 7.16344 32 16 32C24.8366 32 32 24.8366 32 16Z"
        fill="#2E0700"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M15.9997 5.33301L26.6664 15.9997L15.9997 26.6663L5.33301 15.9997L15.9997 5.33301ZM15.9997 7.99976L23.9997 15.9998L15.9997 23.9998L7.99967 15.9998L15.9997 7.99976ZM21.333 15.9995L15.9997 10.6661L10.6664 15.9995L15.9997 21.3328L21.333 15.9995ZM18.6664 15.9996L15.9997 13.3329L13.333 15.9996L15.9997 18.6662L18.6664 15.9996Z"
        fill="#D4FF28"
      />
    </g>
    <defs>
      <clipPath id="clip0_25576_7853">
        <rect width="32" height="32" fill="white" />
      </clipPath>
    </defs>
  </svg>
);

const BaseIcon = ({ size = 32 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 32 32"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
  >
    <circle cx="16" cy="16" r="16" fill="#0052FF" />
    <path
      d="M16.08 6.4C10.72 6.4 6.4 10.72 6.4 16.08C6.4 21.44 10.72 25.76 16.08 25.76C20.88 25.76 24.88 22.4 25.6 17.84H19.04C18.4 19.28 17.36 19.84 16.08 19.84C13.92 19.84 12.24 18.16 12.24 16C12.24 13.84 13.92 12.16 16.08 12.16C17.36 12.16 18.4 12.72 19.04 14.16H25.6C24.88 9.6 20.88 6.4 16.08 6.4Z"
      fill="white"
    />
  </svg>
);

const chainIcons: Partial<
  Record<ChainName, React.ComponentType<{ size?: number }>>
> = {
  mainnet: EthereumIcon,
  sepolia: EthereumIcon,
  hoodi: EthereumIcon,
  base: BaseIcon,
  'base-sepolia': BaseIcon,
  aztec: AztecIcon,
};

export interface ChainIconProps {
  chain: ChainName;
  size?: number;
  className?: string;
}

export function ChainIcon({ chain, size = 32, className }: ChainIconProps) {
  const IconComponent = chainIcons[chain] ?? EthereumIcon;
  return (
    <div
      className={className}
      style={{ display: 'inline-flex', alignItems: 'center' }}
    >
      <IconComponent size={size} />
    </div>
  );
}
