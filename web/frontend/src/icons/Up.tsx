import * as React from 'react';
import { IconSize, getIconSize } from './index';

export const Up = ({
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
      viewBox="0 0 16 16"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      {...props}
    >
      <path
        d="M4.94 10.4697L8 7.4164L11.06 10.4697L12 9.52973L8 5.52973L4 9.52973L4.94 10.4697Z"
        fill={color}
      />
    </svg>
  );
};
