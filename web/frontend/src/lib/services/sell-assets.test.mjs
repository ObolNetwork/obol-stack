import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { TOKEN_ADDRESSES } from '../constants/tokens.ts';
import {
  BASE_SEPOLIA_OBOL_ADDRESS,
  normalizeSellToken,
  reconcileSellPaymentSelection,
  resolveSellAsset,
} from './sell-assets.ts';

describe('normalizeSellToken', () => {
  it('defaults unknown values to USDC', () => {
    assert.equal(normalizeSellToken(undefined), 'USDC');
    assert.equal(normalizeSellToken('USDC'), 'USDC');
    assert.equal(normalizeSellToken('OBOL'), 'OBOL');
  });
});

describe('resolveSellAsset', () => {
  it('resolves Base Sepolia USDC with signing metadata', () => {
    const out = resolveSellAsset('base-sepolia', 'USDC', {});

    assert.equal(out.error, undefined);
    assert.equal(
      out.asset?.address,
      '0x036CbD53842c5426634e7929541eC2318f3dCF7e'
    );
    assert.equal(out.asset?.symbol, 'USDC');
    assert.equal(out.asset?.decimals, 6);
    assert.equal(out.asset?.transferMethod, 'eip3009');
    assert.equal(out.asset?.eip712Name, 'USDC');
    assert.equal(out.asset?.eip712Version, '2');
  });

  it('resolves Ethereum mainnet OBOL without guessing another chain', () => {
    const out = resolveSellAsset('mainnet', 'OBOL', {});

    assert.equal(out.error, undefined);
    assert.equal(out.asset?.address, TOKEN_ADDRESSES.OBOL);
    assert.equal(out.asset?.symbol, 'OBOL');
    assert.equal(out.asset?.decimals, 18);
    assert.equal(out.asset?.transferMethod, 'permit2');
    assert.equal(out.asset?.eip712Name, 'Obol Network');
    assert.equal(out.asset?.eip712Version, '1');
  });

  it('resolves Base Sepolia OBOL with the provided default address', () => {
    const out = resolveSellAsset('base-sepolia', 'OBOL', {});

    assert.equal(out.error, undefined);
    assert.equal(out.asset?.address, BASE_SEPOLIA_OBOL_ADDRESS);
    assert.equal(out.asset?.symbol, 'OBOL');
    assert.equal(out.asset?.decimals, 18);
    assert.equal(out.asset?.transferMethod, 'permit2');
  });

  it('requires a configured address before allowing OBOL on Base Mainnet', () => {
    const out = resolveSellAsset('base', 'OBOL', {});

    assert.equal(out.asset, undefined);
    assert.match(out.error ?? '', /OBOL is not configured/);
  });

  it('treats base-mainnet as Base Mainnet for OBOL availability', () => {
    const out = resolveSellAsset('base-mainnet', 'OBOL', {});

    assert.equal(out.asset, undefined);
    assert.match(out.error ?? '', /OBOL is not configured/);
  });

  it('accepts a per-chain OBOL address from server env', () => {
    const out = resolveSellAsset('base-sepolia', 'OBOL', {
      OBOL_TOKEN_BASE_SEPOLIA: '0x0a09371a8b011d5110656ceBCc70603e53FD2c78',
    });

    assert.equal(
      out.asset?.address,
      '0x0a09371a8b011d5110656ceBCc70603e53FD2c78'
    );
    assert.equal(out.asset?.symbol, 'OBOL');
  });
});

describe('reconcileSellPaymentSelection', () => {
  it('auto-switches Base + OBOL to Ethereum mainnet', () => {
    const out = reconcileSellPaymentSelection('base', 'OBOL', {});

    assert.deepEqual(out, {
      chain: 'mainnet',
      token: 'OBOL',
      autoSwitched: true,
    });
  });

  it('keeps Base Sepolia + OBOL selected', () => {
    const out = reconcileSellPaymentSelection('base-sepolia', 'OBOL', {});

    assert.deepEqual(out, {
      chain: 'base-sepolia',
      token: 'OBOL',
    });
  });
});
