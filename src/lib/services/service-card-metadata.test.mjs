import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { describe, it } from 'node:test';

describe('ServiceCard metadata presentation', () => {
  it('renders registration domains alongside skills and trust metadata', async () => {
    const source = await readFile(
      'src/components/molecules/ServiceCard/ServiceCard.tsx',
      'utf8'
    );

    assert.match(source, /offer\.registration\?\.domains \?\? \[\]/);
    assert.match(source, /Domain: \{domain\}/);
  });

  it('keeps service metadata text above the 10px legibility floor', async () => {
    const source = await readFile(
      'src/components/molecules/ServiceCard/ServiceCard.tsx',
      'utf8'
    );

    assert.doesNotMatch(source, /fontSize: '10px'/);
    assert.doesNotMatch(source, /opacity: 0\.5/);
  });

  it('does not render controller condition internals as card metadata', async () => {
    const source = await readFile(
      'src/components/molecules/ServiceCard/ServiceCard.tsx',
      'utf8'
    );

    assert.doesNotMatch(source, /ModelReady/);
    assert.doesNotMatch(source, /UpstreamHealthy/);
    assert.doesNotMatch(source, /PaymentGateReady/);
    assert.doesNotMatch(source, /RoutePublished/);
    assert.doesNotMatch(source, /ConditionsRow/);
  });

  it('pins the service status pill to the card header edge', async () => {
    const source = await readFile(
      'src/components/molecules/ServiceCard/ServiceCard.tsx',
      'utf8'
    );

    assert.match(source, /const TopRow = styled\('div', \{[\s\S]*width: '100%'/);
    assert.match(source, /const HeaderMeta = styled\('div', \{[\s\S]*flex: '1 1 auto'/);
    assert.match(source, /const HeaderStatus = styled\('div', \{[\s\S]*marginLeft: 'auto'/);
  });
});
