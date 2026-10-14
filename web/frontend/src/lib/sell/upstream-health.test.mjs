import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { resolveUpstreamHealthPath } from './upstream-health.ts';

describe('resolveUpstreamHealthPath', () => {
  it('uses Ollama tags endpoint', () => {
    assert.equal(resolveUpstreamHealthPath('ollama'), '/api/tags');
  });

  it('uses LiteLLM readiness endpoint', () => {
    assert.equal(resolveUpstreamHealthPath('litellm'), '/health/readiness');
  });

  it('defaults to readiness for unknown services in llm', () => {
    assert.equal(resolveUpstreamHealthPath('my-svc'), '/health/readiness');
  });
});
