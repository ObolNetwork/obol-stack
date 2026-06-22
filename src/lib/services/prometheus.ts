/**
 * Prometheus API client for fetching metrics
 */

import { config } from '../config/environment.ts';
import {
  isClusterInternalUrl,
  isUrlReachableFromRuntime,
} from '../utils/cluster-url.ts';

const EMPTY_SYSTEM_METRICS = {
  memory: { total: 0, used: 0, available: 0, percentage: 0 },
  cpu: { usage: 0 },
  disk: { total: 0, used: 0, available: 0, percentage: 0 },
  uptime: 0,
};

interface PrometheusResponse {
  status: string;
  data: {
    resultType: string;
    result: Array<{
      metric: Record<string, string>;
      value?: [number, string];
      values?: Array<[number, string]>;
    }>;
  };
}

export class PrometheusClient {
  private baseUrl: string;

  constructor(baseUrl: string = config.prometheusUrl) {
    this.baseUrl = baseUrl;
  }

  /**
   * Execute a Prometheus query
   */
  private async query(query: string): Promise<PrometheusResponse> {
    if (!isUrlReachableFromRuntime(this.baseUrl)) {
      throw new Error('prometheus unreachable from local dev');
    }

    const url = `${this.baseUrl}/api/v1/query`;
    const params = new URLSearchParams({ query });

    const response = await fetch(`${url}?${params}`, {
      headers: {
        Accept: 'application/json',
      },
    });

    if (!response.ok) {
      throw new Error(`Prometheus query failed: ${response.statusText}`);
    }

    return response.json();
  }

  /**
   * Map of "<namespace>/<offer-name>" -> charged-request count over the
   * last 7d window, summed across x402-verifier replicas. Reads from the
   * `x402:revenue:7d_by_offer` recording rule maintained by the
   * PrometheusRule in internal/embed/infrastructure/base/templates/
   * x402-prometheus-rules.yaml.
   *
   * The rule uses `sum(increase(counter[7d]))` so the value is reset-safe
   * across verifier restarts — DO NOT add an `or sum(counter)` fallback;
   * the raw counter would re-introduce the per-process reset bug the
   * recording rule fixes.
   *
   * Returns an empty Map if Prometheus is unavailable or the metric hasn't
   * been scraped yet — callers render a "—" rather than surface an error.
   */
  async chargedSales7dByOffer(): Promise<Map<string, number>> {
    const result = new Map<string, number>();
    try {
      const response = await this.query('x402:revenue:7d_by_offer');
      for (const row of response.data.result) {
        const ns = row.metric.offer_namespace;
        const name = row.metric.offer_name;
        if (!ns || !name) continue;
        const value = parseFloat(row.value?.[1] ?? '0');
        if (!Number.isFinite(value)) continue;
        result.set(`${ns}/${name}`, value);
      }
    } catch (error) {
      console.warn('[Prometheus] chargedSales7dByOffer failed:', error);
    }
    return result;
  }

  /**
   * Charged-request counts per (offer, chain) over a sliding window. Used by
   * the My Listings EarningsStrip to multiply by per-offer price for an
   * earnings rollup even before we have a settlement-amount metric.
   *
   * Returns Map<"<ns>/<name>", Array<{chain, requests}>>.
   */
  async chargedSalesByOfferAndChain(
    window: '1h' | '24h' | '7d' | '30d' = '7d'
  ): Promise<Map<string, Array<{ chain: string; requests: number }>>> {
    const result = new Map<
      string,
      Array<{ chain: string; requests: number }>
    >();
    // Recording rules shipped in base/templates/x402-prometheus-rules.yaml
    // pre-aggregate the 7d + 24h windows. Falling back to the raw
    // `increase()` query keeps the page working if the rule hasn't
    // propagated yet OR the operator is on a 1h/30d window we don't
    // pre-record.
    const recordingRule =
      window === '24h'
        ? 'x402:revenue:24h_by_offer_chain'
        : window === '7d'
          ? 'x402:revenue:7d_by_offer_chain'
          : null;
    const expr = recordingRule
      ? `${recordingRule} or sum by (offer_namespace, offer_name, chain) (increase(obol_x402_verifier_charged_requests_total[${window}]))`
      : `sum by (offer_namespace, offer_name, chain) (increase(obol_x402_verifier_charged_requests_total[${window}]))`;
    try {
      const response = await this.query(expr);
      for (const row of response.data.result) {
        const ns = row.metric.offer_namespace;
        const name = row.metric.offer_name;
        const chain = row.metric.chain ?? '';
        if (!ns || !name) continue;
        const v = parseFloat(row.value?.[1] ?? '0');
        if (!Number.isFinite(v)) continue;
        const key = `${ns}/${name}`;
        const existing = result.get(key) ?? [];
        existing.push({ chain, requests: v });
        result.set(key, existing);
      }
    } catch (error) {
      console.warn('[Prometheus] chargedSalesByOfferAndChain failed:', error);
    }
    return result;
  }

  /**
   * 24h request counts per offer — used by ListingRow's "Traffic · 24h" cell.
   * Falls back to a raw `increase()` over the 7d window if the recording
   * rule hasn't propagated yet.
   */
  async chargedRequests24hByOffer(): Promise<Map<string, number>> {
    const result = new Map<string, number>();
    try {
      // Reads the same 24h recording rule grouped by offer (sum across chains).
      const response = await this.query(
        'sum by (offer_namespace, offer_name) (x402:revenue:24h_by_offer_chain) or sum by (offer_namespace, offer_name) (increase(obol_x402_verifier_charged_requests_total[24h]))'
      );
      for (const row of response.data.result) {
        const ns = row.metric.offer_namespace;
        const name = row.metric.offer_name;
        if (!ns || !name) continue;
        const v = parseFloat(row.value?.[1] ?? '0');
        if (!Number.isFinite(v)) continue;
        result.set(`${ns}/${name}`, v);
      }
    } catch (error) {
      console.warn('[Prometheus] chargedRequests24hByOffer failed:', error);
    }
    return result;
  }

  /**
   * Map of "<namespace>/<offer-name>/<chain>" -> Unix-seconds timestamp of the
   * last successful charged request observed by the verifier. Reads
   * obol_x402_verifier_last_payment_success_seconds directly (instant query).
   * Returns an empty Map if Prometheus is unavailable or the offer has never
   * been settled — callers render "—" rather than surface an error.
   *
   * Use this on the My Purchases drawer to render "last settled X ago".
   */
  async lastPaymentSuccessByOfferChain(): Promise<Map<string, number>> {
    const result = new Map<string, number>();
    try {
      const response = await this.query(
        'obol_x402_verifier_last_payment_success_seconds'
      );
      for (const row of response.data.result) {
        const ns = row.metric.offer_namespace;
        const name = row.metric.offer_name;
        const chain = row.metric.chain;
        if (!ns || !name || !chain) continue;
        const v = parseFloat(row.value?.[1] ?? '');
        if (!Number.isFinite(v)) continue;
        const key = `${ns}/${name}/${chain}`;
        // If multiple verifier replicas report the same offer/chain, keep
        // the most recent timestamp.
        const prev = result.get(key);
        if (prev === undefined || v > prev) result.set(key, v);
      }
    } catch (error) {
      console.warn(
        '[Prometheus] lastPaymentSuccessByOfferChain failed:',
        error
      );
    }
    return result;
  }

  /**
   * Most-recent successful settlement per offer, as a UNIX seconds gauge.
   * Backed by obol_x402_verifier_last_payment_success_seconds in
   * internal/x402/metrics.go.
   */
  async lastSettlementByOffer(): Promise<Map<string, number>> {
    const result = new Map<string, number>();
    try {
      const response = await this.query(
        'max by (offer_namespace, offer_name) (obol_x402_verifier_last_payment_success_seconds)'
      );
      for (const row of response.data.result) {
        const ns = row.metric.offer_namespace;
        const name = row.metric.offer_name;
        if (!ns || !name) continue;
        const v = parseFloat(row.value?.[1] ?? '0');
        if (!Number.isFinite(v)) continue;
        result.set(`${ns}/${name}`, v);
      }
    } catch (error) {
      console.warn('[Prometheus] lastSettlementByOffer failed:', error);
    }
    return result;
  }

  /**
   * Get system metrics
   */
  async getSystemMetrics() {
    if (!isUrlReachableFromRuntime(this.baseUrl)) {
      return EMPTY_SYSTEM_METRICS;
    }

    try {
      // Get memory metrics
      const memTotal = await this.query('node_memory_MemTotal_bytes');
      const memAvailable = await this.query('node_memory_MemAvailable_bytes');

      // Get CPU metrics (average CPU usage)
      const cpuUsage = await this.query(
        'avg(rate(node_cpu_seconds_total{mode!="idle"}[5m]))'
      );

      // Disk metrics. We query the root filesystem instead of pinning a
      // device name — on most Linux hosts the root device is nvme0n1pN or
      // sdaN, not /dev/vda1 (which is a KVM/virtio name). Filtering by
      // mountpoint="/" and excluding the pseudo-filesystems that
      // node-exporter exposes (tmpfs/overlay/squashfs/...) gives us the
      // real backing disk on baremetal, VMs, and container hosts alike.
      // sum() collapses multiple results into one in case node-exporter
      // returns per-fstype duplicates.
      //
      // Note: this.query() returns an empty result set rather than
      // throwing when Prometheus matches no series, so try/catch is the
      // wrong tool here. We detect "no data" by checking the result array
      // length after the fact and fall back to a broader query.
      const fsExcludes =
        'tmpfs|overlay|squashfs|aufs|fuse.lxcfs|nsfs|devtmpfs|ramfs';
      let diskTotal = await this.query(
        `sum(node_filesystem_size_bytes{mountpoint="/",fstype!~"${fsExcludes}"})`
      );
      let diskAvailable = await this.query(
        `sum(node_filesystem_avail_bytes{mountpoint="/",fstype!~"${fsExcludes}"})`
      );

      // Broader fallback: some node-exporter setups (k3s, containerized
      // hosts) don't expose "/" but do expose the largest real filesystem
      // somewhere else. Pick the largest filesystem by total bytes.
      if (!diskTotal.data.result.length) {
        diskTotal = await this.query(
          `max(node_filesystem_size_bytes{fstype!~"${fsExcludes}"})`
        );
        diskAvailable = await this.query(
          `max(node_filesystem_avail_bytes{fstype!~"${fsExcludes}"})`
        );
      }

      if (!diskTotal.data.result.length) {
        console.warn(
          'Prometheus returned no disk metrics; node-exporter may not be scraping the host filesystem'
        );
      }

      // Get uptime
      const uptime = await this.query('node_boot_time_seconds');

      const totalMemory = parseFloat(
        memTotal.data.result[0]?.value?.[1] || '0'
      );
      const availableMemory = parseFloat(
        memAvailable.data.result[0]?.value?.[1] || '0'
      );
      const usedMemory = totalMemory - availableMemory;

      const totalDisk = parseFloat(diskTotal.data.result[0]?.value?.[1] || '0');
      const availableDisk = parseFloat(
        diskAvailable.data.result[0]?.value?.[1] || '0'
      );
      const usedDisk = totalDisk - availableDisk;

      const cpuPercent =
        parseFloat(cpuUsage.data.result[0]?.value?.[1] || '0') * 100;

      const bootTime = parseFloat(uptime.data.result[0]?.value?.[1] || '0');
      const uptimeSeconds = Date.now() / 1000 - bootTime;

      return {
        memory: {
          total: totalMemory,
          used: usedMemory,
          available: availableMemory,
          percentage: totalMemory > 0 ? (usedMemory / totalMemory) * 100 : 0,
        },
        cpu: {
          usage: cpuPercent,
        },
        disk: {
          total: totalDisk,
          used: usedDisk,
          available: availableDisk,
          percentage: totalDisk > 0 ? (usedDisk / totalDisk) * 100 : 0,
        },
        uptime: uptimeSeconds,
      };
    } catch (error) {
      if (!isClusterInternalUrl(this.baseUrl)) {
        console.error('Failed to fetch system metrics:', error);
      }
      throw error;
    }
  }
}
