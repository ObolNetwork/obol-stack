import * as React from 'react';
import { IconSize, getIconSize } from './index';

export const LowConnection = ({
  size = 's',
  color = 'currentColor',
  ...props
}: {
  size?: IconSize;
  color?: string;
} & React.SVGProps<SVGSVGElement>) => {
  const iconSize = getIconSize(size);
  return (
    <svg
      width={iconSize}
      height={iconSize}
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      {...props}
    >
      <path
        d="M22 5.96401C18.8671 3.51354 15.4562 2.24998 11.9999 2.25C8.54366 2.25002 5.1329 3.51356 2 5.96401L3.31491 7.47465C6.12597 5.3001 9.08526 4.25001 11.9999 4.25C14.9146 4.24999 17.8739 5.30004 20.6849 7.47454L22 5.96401Z"
        fill={color}
      />
      <path
        d="M17.711 10.8911L19.0241 9.38285C14.9165 5.85646 9.27505 5.88752 4.97852 9.38533L6.29229 10.8947C9.86482 8.02147 14.3908 8.04996 17.711 10.8911Z"
        fill={color}
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M12.6432 10.6141C12.5076 10.3882 12.2635 10.25 12 10.25C11.7366 10.25 11.4925 10.3882 11.3569 10.6141L5.35688 20.6141C5.21786 20.8458 5.21422 21.1344 5.34735 21.3695C5.48048 21.6047 5.72979 21.75 6 21.75H18C18.2702 21.75 18.5196 21.6047 18.6527 21.3695C18.7858 21.1344 18.7822 20.8458 18.6432 20.6141L12.6432 10.6141ZM12.75 17.5V14.5H11.25V17.5H12.75ZM12.759 18.5H11.25V20H12.759V18.5Z"
        fill={color}
      />
    </svg>
  );
};
