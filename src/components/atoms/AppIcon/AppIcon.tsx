import React from 'react';
import { AppInstanceType } from '../../../types/dashboard';
import { Box } from '@obolnetwork/obol-ui';

export interface AppIconProps {
  app: AppInstanceType;
  size?: number;
  className?: string;
}

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

const HeliosIcon = () => (
  <Box
    css={{
      width: '$lg',
      height: '$lg',
      borderRadius: '$round',
      backgroundColor: '$secondaryBgDefault',
      backgroundImage:
        'url(https://avatars.githubusercontent.com/u/745163?s=200&v=4)',
      backgroundSize: 'contain',
      backgroundPosition: 'center',
      backgroundRepeat: 'no-repeat',
    }}
  />
);

const ErpcIcon = ({ size = 32 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 32 32"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
  >
    <circle cx="16" cy="16" r="16" fill="#1A1032" />
    <circle cx="16" cy="16" r="6" stroke="#9167E4" strokeWidth="2" />
    <circle cx="7" cy="10" r="2.5" fill="#9167E4" />
    <circle cx="25" cy="10" r="2.5" fill="#9167E4" />
    <circle cx="7" cy="22" r="2.5" fill="#9167E4" />
    <circle cx="25" cy="22" r="2.5" fill="#9167E4" />
    <line
      x1="9.2"
      y1="11.2"
      x2="13"
      y2="13.5"
      stroke="#9167E4"
      strokeWidth="1.5"
    />
    <line
      x1="22.8"
      y1="11.2"
      x2="19"
      y2="13.5"
      stroke="#9167E4"
      strokeWidth="1.5"
    />
    <line
      x1="9.2"
      y1="20.8"
      x2="13"
      y2="18.5"
      stroke="#9167E4"
      strokeWidth="1.5"
    />
    <line
      x1="22.8"
      y1="20.8"
      x2="19"
      y2="18.5"
      stroke="#9167E4"
      strokeWidth="1.5"
    />
  </svg>
);

const appIcons: Record<
  AppInstanceType,
  React.ComponentType<{ size?: number }>
> = {
  ethereum: EthereumIcon,
  aztec: AztecIcon,
  helios: HeliosIcon,
  erpc: ErpcIcon,
};

export function AppIcon({ app, size = 32, className }: AppIconProps) {
  const IconComponent = appIcons[app] || EthereumIcon; // Fallback to Ethereum icon

  return (
    <div
      className={className}
      style={{ display: 'inline-flex', alignItems: 'center' }}
    >
      <IconComponent size={size} />
    </div>
  );
}
