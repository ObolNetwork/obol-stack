import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { pathnameToTopNavId, topNavIdToPath } from './top-nav-route.ts';

describe('pathnameToTopNavId', () => {
  it('maps marketplace and nested slug paths to marketplace', () => {
    assert.equal(pathnameToTopNavId('/marketplace'), 'marketplace');
    assert.equal(
      pathnameToTopNavId('/marketplace/agent-quant/quant'),
      'marketplace'
    );
  });

  it('maps agent paths to agents', () => {
    assert.equal(pathnameToTopNavId('/agents'), 'agents');
    assert.equal(pathnameToTopNavId('/agents/research'), 'agents');
  });

  it('defaults everything else to home', () => {
    assert.equal(pathnameToTopNavId('/'), 'home');
    assert.equal(pathnameToTopNavId('/dashboard'), 'home');
    assert.equal(pathnameToTopNavId(null), 'home');
    assert.equal(pathnameToTopNavId(''), 'home');
  });

  it('does not confuse path prefixes ("/marketplaceX" stays home)', () => {
    assert.equal(pathnameToTopNavId('/marketplaceX'), 'home');
    assert.equal(pathnameToTopNavId('/agentsplus'), 'home');
  });
});

describe('topNavIdToPath', () => {
  it('rounds trip the three known ids', () => {
    assert.equal(topNavIdToPath('home'), '/');
    assert.equal(topNavIdToPath('marketplace'), '/marketplace');
    assert.equal(topNavIdToPath('agents'), '/agents');
  });
});
