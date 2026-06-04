import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  hasCapabilityTaxonomy,
  isOasfService,
  serviceAvailabilityLabel,
} from './agent-registration-capabilities.ts';

describe('isOasfService', () => {
  it('matches OASF case-insensitively', () => {
    assert.equal(isOasfService({ name: 'OASF' }), true);
    assert.equal(isOasfService({ name: 'oasf' }), true);
    assert.equal(isOasfService({ name: 'web' }), false);
  });
});

describe('hasCapabilityTaxonomy', () => {
  it('keeps endpoint-less OASF entries that carry skills', () => {
    assert.equal(
      hasCapabilityTaxonomy({
        name: 'OASF',
        version: '0.8',
        skills: ['natural_language_processing/text_generation'],
      }),
      true
    );
  });

  it('keeps endpoint-less OASF entries that carry domains', () => {
    assert.equal(
      hasCapabilityTaxonomy({
        name: 'OASF',
        version: '0.8',
        domains: ['technology/artificial_intelligence'],
      }),
      true
    );
  });

  it('does not treat ordinary endpoint services as capabilities', () => {
    assert.equal(
      hasCapabilityTaxonomy({
        name: 'web',
        endpoint: 'https://seller.example/services/demo',
        skills: ['ignored'],
      }),
      false
    );
  });
});

describe('serviceAvailabilityLabel', () => {
  it('uses drainEndsAt as the current drain signal', () => {
    assert.equal(
      serviceAvailabilityLabel({
        name: 'OASF',
        drainEndsAt: '2026-05-24T12:00:00Z',
      }),
      'draining until 2026-05-24T12:00:00Z'
    );
  });

  it('supports legacy available=false documents', () => {
    assert.equal(
      serviceAvailabilityLabel({ name: 'OASF', available: false }),
      'unavailable'
    );
  });

  it('returns null for available services without drain metadata', () => {
    assert.equal(serviceAvailabilityLabel({ name: 'OASF' }), null);
  });
});
