import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  deriveOfferDisplayStatus,
  isOperationallyReady,
  isRegistrationPending,
  registeredConditionMessage,
} from './offer-status.ts';

function cond(type, status, message) {
  return { type, status, message: message ?? '' };
}

const OPS_HTTP = [
  cond('UpstreamHealthy', 'True'),
  cond('PaymentGateReady', 'True'),
  cond('RoutePublished', 'True'),
];
const OPS_INFER = [cond('ModelReady', 'True'), ...OPS_HTTP];

function offer(overrides = {}) {
  return {
    name: 'x',
    namespace: 'llm',
    type: 'http',
    status: 'Pending',
    conditions: [],
    ...overrides,
  };
}

describe('isOperationallyReady', () => {
  it('http offer is ready without ModelReady', () => {
    assert.equal(
      isOperationallyReady(offer({ type: 'http', conditions: OPS_HTTP })),
      true
    );
  });

  it('inference offer requires ModelReady', () => {
    assert.equal(
      isOperationallyReady(offer({ type: 'inference', conditions: OPS_HTTP })),
      false
    );
    assert.equal(
      isOperationallyReady(offer({ type: 'inference', conditions: OPS_INFER })),
      true
    );
  });

  it('is false when any operational condition is missing or not True', () => {
    assert.equal(
      isOperationallyReady(
        offer({
          type: 'http',
          conditions: [
            cond('UpstreamHealthy', 'True'),
            cond('PaymentGateReady', 'True'),
          ],
        })
      ),
      false
    );
    assert.equal(
      isOperationallyReady(
        offer({
          type: 'http',
          conditions: [
            cond('UpstreamHealthy', 'True'),
            cond('PaymentGateReady', 'True'),
            cond('RoutePublished', 'False'),
          ],
        })
      ),
      false
    );
  });
});

describe('isRegistrationPending', () => {
  it('is false when registration is not enabled', () => {
    assert.equal(isRegistrationPending(offer({ conditions: OPS_HTTP })), false);
    assert.equal(
      isRegistrationPending(
        offer({ registration: { enabled: false }, conditions: OPS_HTTP })
      ),
      false
    );
  });

  it('is true when registration is enabled and Registered is not True', () => {
    assert.equal(
      isRegistrationPending(
        offer({
          registration: { enabled: true },
          conditions: [...OPS_HTTP, cond('Registered', 'False', 'awaiting tx')],
        })
      ),
      true
    );
  });

  it('is false when registration is enabled and Registered is True', () => {
    assert.equal(
      isRegistrationPending(
        offer({
          registration: { enabled: true },
          conditions: [...OPS_HTTP, cond('Registered', 'True', 'done')],
        })
      ),
      false
    );
  });
});

describe('registeredConditionMessage', () => {
  it('returns the Registered condition message', () => {
    assert.equal(
      registeredConditionMessage(
        offer({
          registration: { enabled: true },
          conditions: [
            cond('Registered', 'False', 'submit with `obol sell register`'),
          ],
        })
      ),
      'submit with `obol sell register`'
    );
  });

  it('returns undefined when the message is absent or blank', () => {
    assert.equal(
      registeredConditionMessage(offer({ conditions: OPS_HTTP })),
      undefined
    );
    assert.equal(
      registeredConditionMessage(
        offer({ conditions: [cond('Registered', 'False', '   ')] })
      ),
      undefined
    );
  });
});

describe('deriveOfferDisplayStatus', () => {
  it('lets draining win over everything', () => {
    assert.equal(
      deriveOfferDisplayStatus(offer({ status: 'Ready' }), {
        isDraining: true,
      }),
      'draining'
    );
  });

  it('is ready when offer.status is Ready', () => {
    assert.equal(
      deriveOfferDisplayStatus(offer({ status: 'Ready' }), {
        isDraining: false,
      }),
      'ready'
    );
  });

  it('is registration-pending when operationally ready but registration has not landed', () => {
    assert.equal(
      deriveOfferDisplayStatus(
        offer({
          type: 'http',
          status: 'Pending',
          registration: { enabled: true },
          conditions: [
            ...OPS_HTTP,
            cond('Registered', 'False', 'awaiting external'),
          ],
        }),
        { isDraining: false }
      ),
      'registration-pending'
    );

    assert.equal(
      deriveOfferDisplayStatus(
        offer({
          type: 'inference',
          status: 'Pending',
          registration: { enabled: true },
          conditions: [...OPS_INFER, cond('Registered', 'False')],
        }),
        { isDraining: false }
      ),
      'registration-pending'
    );
  });

  it('is pending when registration is disabled even if operationally ready', () => {
    assert.equal(
      deriveOfferDisplayStatus(
        offer({ type: 'http', status: 'Pending', conditions: OPS_HTTP }),
        { isDraining: false }
      ),
      'pending'
    );
  });

  it('is pending when registration is enabled but operational readiness is incomplete', () => {
    assert.equal(
      deriveOfferDisplayStatus(
        offer({
          type: 'http',
          status: 'Pending',
          registration: { enabled: true },
          conditions: [
            cond('UpstreamHealthy', 'True'),
            cond('Registered', 'False'),
          ],
        }),
        { isDraining: false }
      ),
      'pending'
    );
  });
});
