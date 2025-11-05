import * as React from 'react';
import { IconSize, getIconSize } from './index';

export const Close = ({
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
      xmlns="http://www.w3.org/2000/svg"
      width={iconSize}
      height={iconSize}
      fill={color}
      viewBox="0 0 16 16"
      {...props}
    >
      <path
        d="M12.6663 4.27301L11.7263 3.33301L7.99967 7.05967L4.27301 3.33301L3.33301 4.27301L7.05967 7.99967L3.33301 11.7263L4.27301 12.6663L7.99967 8.93967L11.7263 12.6663L12.6663 11.7263L8.93967 7.99967L12.6663 4.27301Z"
        fill={color}
      />
    </svg>
  );
};
