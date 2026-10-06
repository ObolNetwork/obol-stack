import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { describe, it } from 'node:test';

const credentialReadRoutes = [
  'src/app/api/agents/token/route.ts',
  'src/app/api/agents/wallet/route.ts',
  'src/app/api/agents/wallets/route.ts',
];

describe('credential read API routes', () => {
  for (const route of credentialReadRoutes) {
    it(`${route} checks origin before client access`, async () => {
      const source = await readFile(route, 'utf8');
      const guardIndex = source.indexOf('rejectCrossOriginCredentialRead');
      assert.notEqual(guardIndex, -1);

      const clientIndex = source.search(/new (AgentClient|OpenClawClient)\(/);
      assert.notEqual(clientIndex, -1);
      assert.ok(
        guardIndex < clientIndex,
        'origin guard must run before Kubernetes-backed client access'
      );
    });
  }
});
