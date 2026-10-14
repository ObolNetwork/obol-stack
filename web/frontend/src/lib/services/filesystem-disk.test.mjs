import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  pickRootFilesystemUsage,
  parseFilesystemSamples,
} from './filesystem-disk.ts';

describe('pickRootFilesystemUsage', () => {
  it('prefers mountpoint="/" when size and avail are paired', () => {
    const total = [
      { mountpoint: '/', fstype: 'ext4', bytes: 500 * 1024 ** 3 },
      {
        mountpoint: '/var/lib/kubelet/pods',
        fstype: 'ext4',
        bytes: 2000 * 1024 ** 3,
      },
    ];
    const avail = [
      { mountpoint: '/', fstype: 'ext4', bytes: 300 * 1024 ** 3 },
      {
        mountpoint: '/var/lib/kubelet/pods',
        fstype: 'ext4',
        bytes: 100 * 1024 ** 3,
      },
    ];
    const out = pickRootFilesystemUsage(total, avail);
    assert.equal(out?.mountpoint, '/');
    assert.equal(out?.total, 500 * 1024 ** 3);
    assert.equal(out?.used, 200 * 1024 ** 3);
  });

  it('does not mix total from one mount with avail from another', () => {
    const total = [{ mountpoint: '/data', fstype: 'xfs', bytes: 1000 }];
    const avail = [{ mountpoint: '/other', fstype: 'xfs', bytes: 900 }];
    assert.equal(pickRootFilesystemUsage(total, avail), null);
  });

  it('skips fakeowner mountpoint="/" and prefers k3s disk', () => {
    const total = [
      {
        mountpoint: '/',
        fstype: 'fakeowner',
        device: '/run/host_mark/Users',
        bytes: 254633621585920,
      },
      {
        mountpoint: '/var/lib/rancher/k3s',
        fstype: 'ext4',
        device: '/dev/vda1',
        bytes: 977896124416,
      },
    ];
    const avail = [
      {
        mountpoint: '/',
        fstype: 'fakeowner',
        device: '/run/host_mark/Users',
        bytes: 140191431917568,
      },
      {
        mountpoint: '/var/lib/rancher/k3s',
        fstype: 'ext4',
        device: '/dev/vda1',
        bytes: 848655142912,
      },
    ];
    const out = pickRootFilesystemUsage(total, avail);
    assert.equal(out?.mountpoint, '/var/lib/rancher/k3s');
    assert.equal(out?.total, 977896124416);
  });

  it('skips Docker Desktop fakeowner host bind mounts', () => {
    const total = [
      {
        mountpoint: '/data',
        fstype: 'fakeowner',
        device: '/run/host_mark/Users',
        bytes: 254633621585920,
      },
      {
        mountpoint: '/var/lib/rancher/k3s',
        fstype: 'ext4',
        device: '/dev/vda1',
        bytes: 977896124416,
      },
    ];
    const avail = [
      {
        mountpoint: '/data',
        fstype: 'fakeowner',
        device: '/run/host_mark/Users',
        bytes: 140191431917568,
      },
      {
        mountpoint: '/var/lib/rancher/k3s',
        fstype: 'ext4',
        device: '/dev/vda1',
        bytes: 848655142912,
      },
    ];
    const out = pickRootFilesystemUsage(total, avail);
    assert.equal(out?.mountpoint, '/var/lib/rancher/k3s');
    assert.equal(out?.total, 977896124416);
  });
});

describe('parseFilesystemSamples', () => {
  it('maps prometheus rows to mountpoint samples', () => {
    const rows = parseFilesystemSamples([
      {
        metric: { mountpoint: '/', fstype: 'ext4', device: '/dev/vda1' },
        value: [1, '1073741824'],
      },
    ]);
    assert.equal(rows[0].bytes, 1073741824);
  });
});
