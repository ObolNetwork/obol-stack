const { execSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const envFile = path.resolve('.env.local');

let existing = '';
if (fs.existsSync(envFile)) {
  existing = fs.readFileSync(envFile, 'utf-8');
}

function run(cmd) {
  try {
    return execSync(cmd).toString().trim();
  } catch {
    return 'unknown';
  }
}

const commit = run('git rev-parse --short HEAD');
const tag = run('git describe --tags --abbrev=0');

// Remove previous NEXT_PUBLIC_* lines
const filtered = existing
  .split('\n')
  .filter(
    line =>
      !line.startsWith('NEXT_PUBLIC_COMMIT_SHA') &&
      !line.startsWith('NEXT_PUBLIC_TAG_VERSION')
  )
  .join('\n');

// Append new values
const content = `${filtered}${filtered ? '\n' : ''}NEXT_PUBLIC_COMMIT_SHA=${commit}\nNEXT_PUBLIC_TAG_VERSION=${tag}\n`;

fs.writeFileSync(envFile, content);
console.log('Updated .env.local with commit + tag (existing vars preserved)');
