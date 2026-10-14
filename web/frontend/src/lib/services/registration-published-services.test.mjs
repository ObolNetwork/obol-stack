import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { listPublishedRegistrationServices } from './registration-published-services.ts';

describe('listPublishedRegistrationServices', () => {
  it('pairs web endpoints with the following OASF block', () => {
    const rows = listPublishedRegistrationServices([
      {
        name: 'web',
        endpoint:
          'https://tunnel.example.com/services/demo-hello',
      },
      { name: 'OASF', version: '0.8', skills: ['hello'] },
      {
        name: 'web',
        endpoint:
          'https://tunnel.example.com/services/agent-v0-10-0',
      },
      {
        name: 'OASF',
        version: '0.8',
        skills: ['ethereum-networks', 'gas'],
      },
    ]);

    assert.equal(rows.length, 2);
    assert.equal(rows[0].name, 'demo-hello');
    assert.deepEqual(rows[0].skills, ['hello']);
    assert.equal(rows[1].name, 'agent-v0-10-0');
    assert.deepEqual(rows[1].skills, ['ethereum-networks', 'gas']);
  });
});
