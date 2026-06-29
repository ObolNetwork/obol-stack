/**
 * Pick host root disk usage from node_exporter filesystem metrics.
 * Pairs size + avail on the same mountpoint (never mixes unrelated series).
 *
 * Grounded in obol-stack ops: Docker Desktop k3d often reports a fakeowner `/`
 * bind mount (see obol-stack `CLAUDE.md` pitfalls §9). We skip fakeowner/host_mark
 * and prefer `/var/lib/rancher/k3s` when `/` is absent or bogus — see
 * `filesystem-disk.test.mjs` and live verification on k3d node `/dev/vda1`.
 *
 * Metrics: `node_filesystem_size_bytes`, `node_filesystem_avail_bytes`
 * (prometheus-node-exporter in the monitoring namespace).
 */

export interface FilesystemSample {
  mountpoint: string;
  fstype?: string;
  device?: string;
  bytes: number;
}

export interface FilesystemDiskUsage {
  total: number;
  available: number;
  used: number;
  percentage: number;
  mountpoint: string;
}

const EXCLUDED_FSTYPE =
  /^(tmpfs|overlay|squashfs|autofs|fuse\.lxcfs|aufs|nsfs|devtmpfs|ramfs|fakeowner)$/i;
const EXCLUDED_MOUNT = /^\/var\/lib\/kubelet|^\/run\/|^\/sys\/|^\/proc\//;

/** When `/` is absent, prefer a stable k3s node mount over "largest". */
const PREFERRED_MOUNTS = [
  '/',
  '/var/lib/rancher/k3s',
  '/var/log',
  '/k3d/images',
] as const;

function indexByMountpoint(
  rows: FilesystemSample[]
): Map<string, FilesystemSample> {
  const out = new Map<string, FilesystemSample>();
  for (const row of rows) {
    if (!row.mountpoint || !Number.isFinite(row.bytes)) continue;
    out.set(row.mountpoint, row);
  }
  return out;
}

function isCandidateMount(
  mountpoint: string,
  fstype?: string,
  device?: string
): boolean {
  if (!mountpoint) return false;
  if (device && device.includes('host_mark')) return false;
  if (fstype && EXCLUDED_FSTYPE.test(fstype)) return false;
  if (mountpoint === '/') return true;
  if (EXCLUDED_MOUNT.test(mountpoint)) return false;
  return !mountpoint.includes('/kubelet/');
}

function usageForMount(
  mountpoint: string,
  totalRows: FilesystemSample[],
  availRows: FilesystemSample[]
): FilesystemDiskUsage | null {
  const totals = indexByMountpoint(totalRows);
  const avails = indexByMountpoint(availRows);
  const total = totals.get(mountpoint);
  const avail = avails.get(mountpoint);
  if (!total || !avail) return null;

  const totalBytes = total.bytes;
  const availableBytes = avail.bytes;
  const used = totalBytes - availableBytes;
  return {
    mountpoint,
    total: totalBytes,
    available: availableBytes,
    used,
    percentage: totalBytes > 0 ? (used / totalBytes) * 100 : 0,
  };
}

function candidateMountpoints(
  totalRows: FilesystemSample[],
  availRows: FilesystemSample[]
): string[] {
  const mounts = new Set<string>();
  for (const row of [...totalRows, ...availRows]) {
    if (
      row.mountpoint &&
      isCandidateMount(row.mountpoint, row.fstype, row.device)
    ) {
      mounts.add(row.mountpoint);
    }
  }
  return [...mounts];
}

function preferredMountRank(mountpoint: string): number {
  const idx = PREFERRED_MOUNTS.indexOf(
    mountpoint as (typeof PREFERRED_MOUNTS)[number]
  );
  return idx === -1 ? PREFERRED_MOUNTS.length : idx;
}

/** Prefer `/`, then known k3s paths, then the largest paired filesystem. */
export function pickRootFilesystemUsage(
  totalRows: FilesystemSample[],
  availRows: FilesystemSample[]
): FilesystemDiskUsage | null {
  const rootMeta = totalRows.find(r => r.mountpoint === '/');
  if (rootMeta && isCandidateMount('/', rootMeta.fstype, rootMeta.device)) {
    const root = usageForMount('/', totalRows, availRows);
    if (root) return root;
  }

  const candidates = candidateMountpoints(totalRows, availRows)
    .map(mp => usageForMount(mp, totalRows, availRows))
    .filter((u): u is FilesystemDiskUsage => u !== null)
    .sort((a, b) => {
      const rank =
        preferredMountRank(a.mountpoint) - preferredMountRank(b.mountpoint);
      if (rank !== 0) return rank;
      return b.total - a.total;
    });

  return candidates[0] ?? null;
}

export function parseFilesystemSamples(
  rows: Array<{
    metric: Record<string, string>;
    value?: [number, string];
  }>
): FilesystemSample[] {
  return rows
    .map(row => ({
      mountpoint: row.metric.mountpoint ?? '',
      fstype: row.metric.fstype,
      device: row.metric.device,
      bytes: parseFloat(row.value?.[1] ?? '0'),
    }))
    .filter(row => row.mountpoint && Number.isFinite(row.bytes));
}

/** fstype values node_exporter should skip when listing real disks. */
export const FILESYSTEM_FSTYPE_EXCLUDES =
  'tmpfs|overlay|squashfs|aufs|fuse.lxcfs|nsfs|devtmpfs|ramfs|autofs|fakeowner';

/** Limit PromQL payload — full filesystem scrape is slow over port-forward. */
export const FILESYSTEM_MOUNTPOINT_QUERY =
  'mountpoint=~"/|/var/lib/rancher/k3s|/var/log|/k3d/images"';
