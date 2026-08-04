import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  buildCliCommand,
  collapseDataUrisForDisplay,
  isHttpLogoUrl,
} from './storefront-utils.ts';

const base = {
  displayName: 'Agent Services',
  tagline: 'Unlock Agent and API services with digital payments.',
  theme: 'light',
  accent: '#2FE4AB',
  logoDataUrl: null,
};

function changed(...fields) {
  return new Set(fields);
}

describe('storefront branding CLI', () => {
  it('accepts only http(s) logo URLs as remote logos', () => {
    assert.equal(isHttpLogoUrl('https://cdn.example.com/logo.png'), true);
    assert.equal(isHttpLogoUrl('http://cdn.example.com/logo.png'), true);
    assert.equal(isHttpLogoUrl('data:image/png;base64,aaa'), false);
    assert.equal(isHttpLogoUrl('/relative.png'), false);
    assert.equal(isHttpLogoUrl(null), false);
  });

  it('emits only --logo-url for an https logo change', () => {
    const cmd = buildCliCommand(
      { ...base, logoDataUrl: 'https://cdn.example.com/logo.png' },
      changed('logoDataUrl')
    );
    assert.match(cmd, /obol sell info set/);
    assert.match(cmd, /--logo-url 'https:\/\/cdn\.example\.com\/logo\.png'/);
    assert.doesNotMatch(cmd, /--logo-file/);
    assert.doesNotMatch(cmd, /--display-name|--theme|--accent|--tagline/);
  });

  it('emits only --logo-url for a data-URI logo change', () => {
    const dataUri = 'data:image/png;base64,aaa';
    const cmd = buildCliCommand(
      { ...base, logoDataUrl: dataUri },
      changed('logoDataUrl')
    );
    assert.match(cmd, /--logo-url 'data:image\/png;base64,aaa'/);
    assert.doesNotMatch(cmd, /--logo-file/);
  });

  it('resets logo with sell info reset --logo-url when cleared', () => {
    const cmd = buildCliCommand(
      { ...base, logoDataUrl: null },
      changed('logoDataUrl')
    );
    assert.equal(cmd, "obol sell info reset \\\n  --logo-url");
  });

  it('omits logo flags when logo is unchanged', () => {
    const cmd = buildCliCommand(
      { ...base, displayName: 'Acme', logoDataUrl: 'https://x/y.png' },
      changed('displayName')
    );
    assert.match(cmd, /--display-name 'Acme'/);
    assert.doesNotMatch(cmd, /logo/);
  });

  it('collapses only the displayed data URI to first 10 and last 5 characters', () => {
    const dataUri = 'data:image/png;base64,abcdefghijklmnopqrstuvwxyz1234567890';
    const command = `obol sell info set --logo-url '${dataUri}'`;
    const collapsed = collapseDataUrisForDisplay(command);

    assert.equal(
      collapsed,
      `obol sell info set --logo-url '${dataUri.slice(0, 10)}…${dataUri.slice(-5)}'`
    );
    assert.equal(command.includes(dataUri), true);
  });

  it('does not collapse normal image URLs', () => {
    const command =
      "obol sell info set --logo-url 'https://cdn.example.com/logo.png'";
    assert.equal(collapseDataUrisForDisplay(command), command);
  });
});
