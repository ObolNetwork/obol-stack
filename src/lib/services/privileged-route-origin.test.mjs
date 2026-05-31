import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { describe, it } from 'node:test';

async function read(path) {
  return readFile(path, 'utf8');
}

function assertGuardBefore(source, guard, sensitivePattern, message) {
  const guardIndex = source.indexOf(guard);
  assert.notEqual(guardIndex, -1, `${guard} is missing`);

  const sensitiveIndex = source.search(sensitivePattern);
  assert.notEqual(sensitiveIndex, -1, `${sensitivePattern} is missing`);

  assert.ok(guardIndex < sensitiveIndex, message);
}

describe('privileged route origin guards', () => {
  it('guards agent chat before parsing body or loading gateway credentials', async () => {
    const source = await read('src/app/api/agents/chat/route.ts');

    assertGuardBefore(
      source,
      'rejectCrossOrigin(request)',
      /request\.json\(\)/,
      'origin guard must run before parsing agent chat requests'
    );
    assertGuardBefore(
      source,
      'rejectCrossOrigin(request)',
      /getGatewayToken/,
      'origin guard must run before loading agent gateway tokens'
    );
  });

  it('guards OpenClaw chat before parsing body or loading gateway credentials', async () => {
    const source = await read('src/app/api/openclaw/chat/route.ts');

    assertGuardBefore(
      source,
      'rejectCrossOrigin(request)',
      /request\.json\(\)/,
      'origin guard must run before parsing OpenClaw chat requests'
    );
    assertGuardBefore(
      source,
      'rejectCrossOrigin(request)',
      /getGatewayToken/,
      'origin guard must run before loading OpenClaw gateway tokens'
    );
  });

  it('guards ServiceOffer create and drain before Kubernetes writes', async () => {
    for (const route of [
      'src/app/api/sell/create/route.ts',
      'src/app/api/sell/stop/route.ts',
    ]) {
      const source = await read(route);

      assertGuardBefore(
        source,
        'rejectCrossOrigin(request)',
        /new SellClient\(/,
        `${route} must guard before constructing the Kubernetes-backed client`
      );
    }
  });

  it('guards ServiceOffer pause, resume, and delete before Kubernetes writes', async () => {
    const source = await read('src/app/api/sell/[name]/route.ts');
    const guardCount = source.match(/rejectCrossOrigin\(req\)/g)?.length ?? 0;

    assert.equal(guardCount, 2);
    assertGuardBefore(
      source,
      'rejectCrossOrigin(req)',
      /new SellClient\(/,
      'origin guard must run before ServiceOffer mutation clients'
    );
  });
});

describe('chat message rendering', () => {
  it('renders agent output as escaped React text, not trusted HTML', async () => {
    for (const file of [
      'src/components/molecules/AgentChatSidebar/AgentChatSidebar.tsx',
      'src/components/molecules/OpenClawChatSidebar/OpenClawChatSidebar.tsx',
    ]) {
      const source = await read(file);

      assert.doesNotMatch(source, /dangerouslySetInnerHTML|innerHTML/);
      assert.match(source, /\{msg\.content\}/);
    }
  });
});
