import { NextResponse } from 'next/server';
import { isAddress } from 'viem';
import { BASE_SEPOLIA_OBOL_DEFAULT } from '../../../../lib/constants/base-sepolia-obol';

export const dynamic = 'force-dynamic';

function readAddress(value: string | undefined): `0x${string}` | undefined {
  if (!value || !isAddress(value)) {
    return undefined;
  }

  return value as `0x${string}`;
}

export async function GET() {
  return NextResponse.json({
    ...BASE_SEPOLIA_OBOL_DEFAULT,
    claimAmountLabel:
      process.env.BASE_SEPOLIA_OBOL_FAUCET_AMOUNT ??
      process.env.NEXT_PUBLIC_BASE_SEPOLIA_OBOL_FAUCET_AMOUNT ??
      BASE_SEPOLIA_OBOL_DEFAULT.claimAmountLabel,
    tokenAddress: readAddress(
      process.env.BASE_SEPOLIA_OBOL_TOKEN_ADDRESS ??
        process.env.NEXT_PUBLIC_BASE_SEPOLIA_OBOL_TOKEN_ADDRESS
    ),
    faucetAddress: readAddress(
      process.env.BASE_SEPOLIA_OBOL_FAUCET_ADDRESS ??
        process.env.NEXT_PUBLIC_BASE_SEPOLIA_OBOL_FAUCET_ADDRESS
    ),
  });
}
