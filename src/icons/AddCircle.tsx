import * as React from 'react';
import { IconSize, getIconSize } from './index';

export const AddCircle = ({
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
        d="M8.00065 1.3335C4.32065 1.3335 1.33398 4.32016 1.33398 8.00016C1.33398 11.6802 4.32065 14.6668 8.00065 14.6668C11.6807 14.6668 14.6673 11.6802 14.6673 8.00016C14.6673 4.32016 11.6807 1.3335 8.00065 1.3335ZM11.334 8.66683H8.66732V11.3335H7.33398V8.66683H4.66732V7.3335H7.33398V4.66683H8.66732V7.3335H11.334V8.66683Z"
        fill={color}
      />
    </svg>
  );
};
