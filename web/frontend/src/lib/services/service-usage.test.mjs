import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  sumChargedRequests24h,
  formatRelativeAgo,
  formatUsageCount,
  formatUsageDisplay,
  offersForAgent,
  sumChargedRequests24hForAgent,
} from './service-usage.ts';

describe('service-usage', () => {
  it('formatUsageCount treats null as zero', () => {
    assert.equal(formatUsageCount(null), 0);
    assert.equal(formatUsageCount(undefined), 0);
    assert.equal(formatUsageCount(3), 3);
  });

  it('formatUsageDisplay returns em dash when missing', () => {
    assert.equal(formatUsageDisplay(null), '—');
    assert.equal(formatUsageDisplay(undefined), '—');
    assert.equal(formatUsageDisplay(42), '42');
  });

  it('sums charged requests24h across offers', () => {
    const total = sumChargedRequests24h([
      { usage: { requests24h: 10 } },
      { usage: { requests24h: 5 } },
      { usage: {} },
    ]);
    assert.equal(total, 15);
  });

  it('filters offers per agent instance', () => {
    const offers = [
      {
        agentLink: { runtime: 'hermes', id: 'obol-agent', namespace: 'x' },
        usage: { requests24h: 10 },
      },
      {
        agentLink: { runtime: 'hermes', id: 'other', namespace: 'x' },
        usage: { requests24h: 5 },
      },
      { usage: { requests24h: 3 } },
    ];
    const hermes = {
      runtime: 'hermes',
      id: 'obol-agent',
      isSubAgent: false,
    };
    assert.equal(
      sumChargedRequests24hForAgent(offers, hermes, {
        receiveLegacyOrphans: true,
      }),
      13
    );
    assert.equal(
      sumChargedRequests24hForAgent(offers, hermes, {
        receiveLegacyOrphans: false,
      }),
      10
    );
    assert.equal(
      sumChargedRequests24hForAgent(offers, {
        runtime: 'hermes',
        id: 'other',
        isSubAgent: true,
      }),
      5
    );
  });

  it('legacy orphans only when receiveLegacyOrphans is set', () => {
    const offers = [{ usage: { requests24h: 7 } }];
    const agent = { runtime: 'hermes', id: 'obol-agent', isSubAgent: false };
    assert.equal(offersForAgent(offers, agent).length, 0);
    assert.equal(
      offersForAgent(offers, agent, { receiveLegacyOrphans: true }).length,
      1
    );
  });

  it('formatRelativeAgo returns em dash when missing', () => {
    assert.equal(formatRelativeAgo(null), '—');
    assert.equal(formatRelativeAgo(0), '—');
  });
});
