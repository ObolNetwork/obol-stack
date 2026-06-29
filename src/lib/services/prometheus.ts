/**
 * Prometheus API client for fetching metrics
 */

import { config } from '../config/environment.ts';
import {
  FILESYSTEM_FSTYPE_EXCLUDES,
  FILESYSTEM_MOUNTPOINT_QUERY,
  parseFilesystemSamples,
  pickRootFilesystemUsage,
} from './filesystem-disk.ts';
import { isUrlReachableFromRuntime } from '../utils/cluster-url.ts';

const EMPTY_SYSTEM_METRICS = {
  live: false,
  memory: {
    total: 0,
    used: 0,
    available: 0,
    percentage: 0,
    queried: false,
  },
  cpu: { usage: 0, queried: false },
  disk: { total: 0, used: 0, available: 0, percentage: 0, paired: false },
  bootTimeAvailable: false,
  uptime: 0,
};

const PROM_QUERY_TIMEOUT_MS = 15_000;

function errorCauseCode(error: unknown): string | undefined {
  const seen = new Set<unknown>();
  let current: unknown = error;
  while (current && !seen.has(current)) {
    seen.add(current);
    if (typeof current === 'object' && current !== null && 'code' in current) {
      const code = (current as { code?: unknown }).code;
      if (typeof code === 'string') return code;
    }
    current = current instanceof Error ? current.cause : undefined;
  }
  return undefined;
}

function prometheusFailureHint(error: unknown): string {
  if (
    error instanceof Error &&
    error.message === 'prometheus unreachable from local dev'
  ) {
    return 'unreachable from local dev (set NEXT_PUBLIC_PROMETHEUS_URL=http://localhost:9090)';
  }
  const code = errorCauseCode(error);
  if (
    code === 'ECONNRESET' ||
    code === 'ECONNREFUSED' ||
    code === 'ENOTFOUND'
  ) {
    return `Prometheus port-forward dropped (${code}) — restart pnpm run dev or run: kubectl port-forward -n monitoring svc/monitoring-kube-prometheus-prometheus 9090:9090`;
  }
  if (
    error instanceof Error &&
    (error.name === 'TimeoutError' ||
      error.name === 'AbortError' ||
      error.message.includes('aborted due to timeout'))
  ) {
    return 'Prometheus query timed out — port-forward may be wedged; restart pnpm run dev';
  }
  if (error instanceof Error) return error.message;
  return String(error);
}

function warnPrometheusFailure(label: string, error: unknown): void {
  console.warn(`[Prometheus] ${label} failed: ${prometheusFailureHint(error)}`);
}

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
      signal: AbortSignal.timeout(PROM_QUERY_TIMEOUT_MS),
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
      warnPrometheusFailure('chargedSales7dByOffer', error);
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
      warnPrometheusFailure('chargedSalesByOfferAndChain', error);
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
      warnPrometheusFailure('chargedRequests24hByOffer', error);
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
      warnPrometheusFailure('lastPaymentSuccessByOfferChain', error);
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
      warnPrometheusFailure('lastSettlementByOffer', error);
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
      const fsFilter = `fstype!~"${FILESYSTEM_FSTYPE_EXCLUDES}",${FILESYSTEM_MOUNTPOINT_QUERY}`;
      const settled = await Promise.allSettled([
        this.query('node_memory_MemTotal_bytes'),
        this.query('node_memory_MemAvailable_bytes'),
        this.query('avg(rate(node_cpu_seconds_total{mode!="idle"}[5m]))'),
        this.query(`node_filesystem_size_bytes{${fsFilter}}`),
        this.query(`node_filesystem_avail_bytes{${fsFilter}}`),
        this.query('node_boot_time_seconds'),
      ]);

      const firstValue = (index: number): string => {
        const result = settled[index];
        if (result.status !== 'fulfilled') {
          warnPrometheusFailure(`getSystemMetrics[${index}]`, result.reason);
          return '0';
        }
        return result.value.data.result[0]?.value?.[1] ?? '0';
      };

      const hasSeries = (index: number): boolean => {
        const result = settled[index];
        return (
          result?.status === 'fulfilled' &&
          (result.value.data.result?.length ?? 0) > 0
        );
      };

      const queryOk = (index: number) => settled[index]?.status === 'fulfilled';

      const diskTotalRes =
        settled[3].status === 'fulfilled' ? settled[3].value : null;
      const diskAvailRes =
        settled[4].status === 'fulfilled' ? settled[4].value : null;

      const diskUsage =
        diskTotalRes && diskAvailRes
          ? pickRootFilesystemUsage(
              parseFilesystemSamples(diskTotalRes.data.result),
              parseFilesystemSamples(diskAvailRes.data.result)
            )
          : null;
      if (!diskUsage) {
        console.warn(
          'Prometheus returned no paired disk metrics; node-exporter may not be scraping the host filesystem'
        );
      }

      const totalMemory = parseFloat(firstValue(0));
      const availableMemory = parseFloat(firstValue(1));
      const usedMemory = totalMemory - availableMemory;

      const totalDisk = diskUsage?.total ?? 0;
      const availableDisk = diskUsage?.available ?? 0;
      const usedDisk = diskUsage?.used ?? 0;

      const cpuPercent = parseFloat(firstValue(2)) * 100;

      const bootTime = parseFloat(firstValue(5));
      const bootTimeAvailable = queryOk(5) && bootTime > 0;
      const uptimeSeconds = bootTimeAvailable
        ? Date.now() / 1000 - bootTime
        : 0;

      const live =
        (queryOk(0) && totalMemory > 0) ||
        hasSeries(2) ||
        (queryOk(3) && queryOk(4) && diskUsage != null);

      return {
        live,
        memory: {
          total: totalMemory,
          used: usedMemory,
          available: availableMemory,
          percentage: totalMemory > 0 ? (usedMemory / totalMemory) * 100 : 0,
          queried: queryOk(0) && totalMemory > 0,
        },
        cpu: {
          usage: cpuPercent,
          queried: hasSeries(2),
        },
        disk: {
          total: totalDisk,
          used: usedDisk,
          available: availableDisk,
          percentage: diskUsage?.percentage ?? 0,
          paired: diskUsage != null,
        },
        bootTimeAvailable,
        uptime: uptimeSeconds,
      };
    } catch (error) {
      warnPrometheusFailure('getSystemMetrics', error);
      return EMPTY_SYSTEM_METRICS;
    }
  }
}
