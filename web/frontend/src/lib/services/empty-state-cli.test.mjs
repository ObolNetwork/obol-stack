import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const FILES = {
  registration:
    'src/components/molecules/AgentRegistrationCard/AgentRegistrationCard.tsx',
  agents: 'src/components/organisms/DashboardContent/DashboardContent.tsx',
  services: 'src/components/organisms/ServiceList/ServiceList.tsx',
};

async function source(name) {
  return readFile(resolve(process.cwd(), FILES[name]), 'utf8');
}

describe('empty-state CLI hints', () => {
  it('points the registrations empty state at obol sell register', async () => {
    const registration = await source('registration');
    // Legacy flags are gone from the empty-state copy.
    assert.equal(registration.includes('--registration.enabled=true'), false);
    assert.equal(registration.includes('--no-register'), false);
    assert.match(registration, /No registrations yet/);
    assert.match(registration, /<code>obol sell register<\/code>/);
    assert.match(registration, /Ethereum Agent Registry/);
  });

  it('uses the current agent creation command', async () => {
    const agents = await source('agents');
    assert.equal(agents.includes('obol hermes onboard'), false);
    assert.match(agents, /obol agent new/);
    assert.match(agents, /obol agent init/);
    assert.match(agents, /sell/i);
  });

  it('mentions the agent service selling path', async () => {
    const services = await source('services');
    assert.doesNotMatch(services, /obol sell inference/);
    assert.match(services, /obol sell agent/);
    assert.match(services, /Created but not live on your storefront yet/);
  });
});
