import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { describe, it } from 'node:test';

describe('agent registration error state', () => {
  it('fetches the public registration resource and surfaces upstream failures', async () => {
    const source = await readFile(
      'src/app/api/agents/registration/route.ts',
      'utf8'
    );

    assert.match(
      source,
      /publicResourceUrl\(tunnelURL, AGENT_REGISTRATION_PATH\)/
    );
    assert.match(source, /response\.status === 404/);
    assert.match(source, /status: 502/);
  });

  it('tracks non-OK registration responses as hook errors', async () => {
    const source = await readFile('src/hooks/useAgents.ts', 'utf8');

    assert.match(source, /const \[error, setError\]/);
    assert.match(source, /if \(!response\.ok\)/);
    assert.match(
      source,
      /throw new Error\(data\?\.error \|\| `HTTP \$\{response\.status\}`\)/
    );
    assert.match(
      source,
      /registration,[\s\S]*tunnelURL,[\s\S]*loading,[\s\S]*error,[\s\S]*refetch: fetchRegistration/
    );
  });

  it('does not render no-registration copy when registration fetch failed', async () => {
    const source = await readFile(
      'src/components/molecules/AgentRegistrationCard/AgentRegistrationCard.tsx',
      'utf8'
    );

    assert.match(source, /<ErrorState/);
    assert.match(source, /!loading && error/);
    assert.match(source, /!loading && !error && !registration/);
  });
});
