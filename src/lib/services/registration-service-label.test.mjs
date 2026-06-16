import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { registrationServiceLabel } from './registration-service-label.ts';

describe('registrationServiceLabel', () => {
  it('uses the last path segment for web endpoints', () => {
    assert.equal(
      registrationServiceLabel(
        {
          name: 'web',
          endpoint:
            'https://tunnel.example.com/services/agent-v0-10-0',
        },
        0
      ),
      'agent-v0-10-0'
    );
  });

  it('falls back when endpoint is missing', () => {
    assert.equal(registrationServiceLabel({ name: 'web' }, 2), 'web #3');
  });
});
