import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const FILES = {
  registration:
    'src/components/molecules/AgentRegistrationCard/AgentRegistrationCard.tsx',
  agents: 'src/components/organisms/AgentList/AgentList.tsx',
  services: 'src/components/organisms/ServiceList/ServiceList.tsx',
};

async function source(name) {
  return readFile(resolve(process.cwd(), FILES[name]), 'utf8');
}

describe('empty-state CLI hints', () => {
  it('does not reference the removed registration.enabled flag', async () => {
    const registration = await source('registration');
    assert.equal(registration.includes('--registration.enabled=true'), false);
    assert.match(registration, /--no-register/);
  });

  it('uses the current agent creation command', async () => {
    const agents = await source('agents');
    assert.equal(agents.includes('obol hermes onboard'), false);
    assert.match(agents, /obol agent new/);
    assert.match(agents, /obol agent init/);
    assert.match(agents, /Sell/);
  });

  it('mentions the agent service selling path', async () => {
    const services = await source('services');
    assert.doesNotMatch(services, /obol sell inference/);
    assert.match(services, /obol sell agent/);
    assert.match(services, /Pending listings/);
  });
});
