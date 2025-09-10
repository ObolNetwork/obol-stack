/**
 * Prometheus API client for fetching metrics
 */

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

  constructor(baseUrl: string = process.env.PROMETHEUS_URL || 'http://localhost:9090') {
    this.baseUrl = baseUrl;
  }

  /**
   * Execute a Prometheus query
   */
  async query(query: string): Promise<PrometheusResponse> {
    const url = `${this.baseUrl}/api/v1/query`;
    const params = new URLSearchParams({ query });
    
    const response = await fetch(`${url}?${params}`, {
      headers: {
        'Accept': 'application/json',
      },
    });

    if (!response.ok) {
      throw new Error(`Prometheus query failed: ${response.statusText}`);
    }

    return response.json();
  }

  /**
   * Execute a Prometheus range query
   */
  async queryRange(query: string, start: Date, end: Date, step: string = '60s'): Promise<PrometheusResponse> {
    const url = `${this.baseUrl}/api/v1/query_range`;
    const params = new URLSearchParams({
      query,
      start: Math.floor(start.getTime() / 1000).toString(),
      end: Math.floor(end.getTime() / 1000).toString(),
      step,
    });
    
    const response = await fetch(`${url}?${params}`, {
      headers: {
        'Accept': 'application/json',
      },
    });

    if (!response.ok) {
      throw new Error(`Prometheus range query failed: ${response.statusText}`);
    }

    return response.json();
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
      const cpuUsage = await this.query('avg(rate(node_cpu_seconds_total{mode!="idle"}[5m]))');
      
      // Get disk metrics
      const diskTotal = await this.query('node_filesystem_size_bytes{mountpoint="/"}');
      const diskAvailable = await this.query('node_filesystem_avail_bytes{mountpoint="/"}');
      
      // Get uptime
      const uptime = await this.query('node_boot_time_seconds');

      const totalMemory = parseFloat(memTotal.data.result[0]?.value?.[1] || '0');
      const availableMemory = parseFloat(memAvailable.data.result[0]?.value?.[1] || '0');
      const usedMemory = totalMemory - availableMemory;
      
      const totalDisk = parseFloat(diskTotal.data.result[0]?.value?.[1] || '0');
      const availableDisk = parseFloat(diskAvailable.data.result[0]?.value?.[1] || '0');
      const usedDisk = totalDisk - availableDisk;
      
      const cpuPercent = parseFloat(cpuUsage.data.result[0]?.value?.[1] || '0') * 100;
      
      const bootTime = parseFloat(uptime.data.result[0]?.value?.[1] || '0');
      const uptimeSeconds = Date.now() / 1000 - bootTime;

      return {
        memory: {
          total: totalMemory,
          used: usedMemory,
          available: availableMemory,
          percentage: (usedMemory / totalMemory) * 100,
        },
        cpu: {
          usage: cpuPercent,
        },
        disk: {
          total: totalDisk,
          used: usedDisk,
          available: availableDisk,
          percentage: (usedDisk / totalDisk) * 100,
        },
        uptime: uptimeSeconds,
      };
    } catch (error) {
      console.error('Failed to fetch system metrics:', error);
      throw error;
    }
  }

  /**
   * Get Kubernetes pod metrics
   */
  async getPodMetrics(namespace?: string) {
    const namespaceFilter = namespace ? `{namespace="${namespace}"}` : '';
    
    try {
      const podStatus = await this.query(`kube_pod_status_phase${namespaceFilter}`);
      const podInfo = await this.query(`kube_pod_info${namespaceFilter}`);
      
      return {
        pods: podStatus.data.result,
        info: podInfo.data.result,
      };
    } catch (error) {
      console.error('Failed to fetch pod metrics:', error);
      throw error;
    }
  }
}