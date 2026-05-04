/**
 * Prometheus API client for fetching metrics
 */

import { config } from '@/lib/config/environment';

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
   * Map of "<namespace>/<offer-name>" -> charged-request count, summed across
   * x402-verifier replicas. Returns an empty Map if Prometheus is unavailable
   * or the metric hasn't been scraped yet — callers render a "—" rather than
   * surface an error to the user.
   */
  async chargedSalesByOffer(): Promise<Map<string, number>> {
    const result = new Map<string, number>();
    try {
      const response = await this.query(
        'sum by (offer_namespace, offer_name) (obol_x402_verifier_charged_requests_total)'
      );
      for (const row of response.data.result) {
        const ns = row.metric.offer_namespace;
        const name = row.metric.offer_name;
        if (!ns || !name) continue;
        const value = parseFloat(row.value?.[1] ?? '0');
        if (!Number.isFinite(value)) continue;
        result.set(`${ns}/${name}`, value);
      }
    } catch (error) {
      console.warn('[Prometheus] chargedSalesByOffer failed:', error);
    }
    return result;
  }

  /**
   * Get system metrics
   */
  async getSystemMetrics() {
    try {
      // Get memory metrics
      const memTotal = await this.query('node_memory_MemTotal_bytes');
      const memAvailable = await this.query('node_memory_MemAvailable_bytes');

      // Get CPU metrics (average CPU usage)
      const cpuUsage = await this.query(
        'avg(rate(node_cpu_seconds_total{mode!="idle"}[5m]))'
      );

      // Get disk metrics - try multiple approaches
      let diskTotal, diskAvailable;
      try {
        // Try with device filter first
        diskTotal = await this.query(
          'node_filesystem_size_bytes{device="/dev/vda1",fstype="ext4"}'
        );
        diskAvailable = await this.query(
          'node_filesystem_avail_bytes{device="/dev/vda1",fstype="ext4"}'
        );
      } catch (e) {
        console.warn(
          'Device-specific disk query failed, trying generic query:',
          e
        );
        try {
          // Fallback to any ext4 filesystem
          diskTotal = await this.query(
            'node_filesystem_size_bytes{fstype="ext4"}'
          );
          diskAvailable = await this.query(
            'node_filesystem_avail_bytes{fstype="ext4"}'
          );
        } catch (e2) {
          console.warn('Generic disk query failed:', e2);
          // Return dummy data for disk if both fail
          diskTotal = { data: { result: [] } };
          diskAvailable = { data: { result: [] } };
        }
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
      console.error('Failed to fetch system metrics:', error);
      throw error;
    }
  }
}
