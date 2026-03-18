import React from 'react';
import { ChainName } from '../../../types/dashboard';

export interface ChainIconProps {
  chain: ChainName;
  size?: number;
  className?: string;
}

const EthereumIcon = ({ size = 32 }: { size: number }) => (
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

const EthereumGrayscaleIcon = ({ size = 32 }: { size: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 32 32"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
  >
    <path
      d="M16 32C24.8366 32 32 24.8366 32 16C32 7.16344 24.8366 0 16 0C7.16344 0 0 7.16344 0 16C0 24.8366 7.16344 32 16 32Z"
      fill="#6B7280"
    />
    <path
      d="M16.498 4V12.87L23.995 16.22L16.498 4Z"
      fill="white"
      fillOpacity="0.5"
    />
    <path
      d="M16.498 4L9 16.22L16.498 12.87V4Z"
      fill="white"
      fillOpacity="0.8"
    />
    <path
      d="M16.498 21.9682V27.9952L24 17.6162L16.498 21.9682Z"
      fill="white"
      fillOpacity="0.5"
    />
    <path
      d="M16.498 27.9952V21.9672L9 17.6162L16.498 27.9952Z"
      fill="white"
      fillOpacity="0.8"
    />
    <path
      d="M16.498 20.5731L23.995 16.2201L16.498 12.8721V20.5731Z"
      fill="white"
      fillOpacity="0.15"
    />
    <path
      d="M9 16.2201L16.498 20.5731V12.8721L9 16.2201Z"
      fill="white"
      fillOpacity="0.5"
    />
  </svg>
);

const BaseIcon = ({ size = 32 }: { size: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 146 146"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
  >
    <circle cx="73" cy="73" r="73" fill="#0052FF" />
    <path
      d="M73.323 123.729C101.617 123.729 124.553 100.832 124.553 72.5875C124.553 44.343 101.617 21.4463 73.323 21.4463C46.4795 21.4463 24.4581 42.0558 22.271 68.2887H89.9859V76.8864H22.271C24.4581 103.119 46.4795 123.729 73.323 123.729Z"
      fill="white"
    />
  </svg>
);

const BaseGrayscaleIcon = ({ size = 32 }: { size: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 146 146"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
  >
    <circle cx="73" cy="73" r="73" fill="#6B7280" />
    <path
      d="M73.323 123.729C101.617 123.729 124.553 100.832 124.553 72.5875C124.553 44.343 101.617 21.4463 73.323 21.4463C46.4795 21.4463 24.4581 42.0558 22.271 68.2887H89.9859V76.8864H22.271C24.4581 103.119 46.4795 123.729 73.323 123.729Z"
      fill="white"
      fillOpacity="0.7"
    />
  </svg>
);

const AztecIcon = ({ size = 32 }: { size: number }) => (
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

const chainIcons: Record<ChainName, React.ComponentType<{ size: number }>> = {
  mainnet: EthereumIcon,
  sepolia: EthereumGrayscaleIcon,
  hoodi: EthereumGrayscaleIcon,
  base: BaseIcon,
  'base-sepolia': BaseGrayscaleIcon,
  aztec: AztecIcon,
};

export function ChainIcon({ chain, size = 32, className }: ChainIconProps) {
  const IconComponent = chainIcons[chain] || EthereumIcon;

  return (
    <div
      className={className}
      style={{ display: 'inline-flex', alignItems: 'center' }}
    >
      <IconComponent size={size} />
    </div>
  );
}

export {
  EthereumIcon,
  EthereumGrayscaleIcon,
  BaseIcon,
  BaseGrayscaleIcon,
  AztecIcon,
};
