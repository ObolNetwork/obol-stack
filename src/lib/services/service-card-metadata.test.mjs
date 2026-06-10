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

  it('renders controller condition chips with hover metadata', async () => {
    const source = await readFile(
      'src/components/molecules/ServiceCard/ServiceCard.tsx',
      'utf8'
    );

    assert.match(source, /ModelReady/);
    assert.match(source, /UpstreamHealthy/);
    assert.match(source, /PaymentGateReady/);
    assert.match(source, /RoutePublished/);
    assert.match(source, /ConditionsRow/);
    assert.match(source, /conditionTitle\(key, offer\.conditions\)/);
  });

  it('pins the service status pill to the card header edge', async () => {
    const source = await readFile(
      'src/components/molecules/ServiceCard/ServiceCard.tsx',
      'utf8'
    );

    assert.match(
      source,
      /const TopRow = styled\('div', \{[\s\S]*width: '100%'/
    );
    assert.match(
      source,
      /const HeaderMeta = styled\('div', \{[\s\S]*flex: '1 1 auto'/
    );
    assert.match(
      source,
      /const HeaderStatus = styled\('div', \{[\s\S]*marginLeft: 'auto'/
    );
  });
});
