import { NextResponse } from 'next/server';
import { PrometheusClient } from '@/lib/services/prometheus';
import { config } from '@/lib/config/environment';

export async function GET() {
  try {
    const prometheus = new PrometheusClient(config.prometheusUrl);
    const metrics = await prometheus.getSystemMetrics();
    
    // Format the response to match the expected dashboard format
    const formattedMetrics = {
      diskUsage: {
        used: Math.round(metrics.disk.used / (1024 * 1024 * 1024)), // Convert to GB
        total: Math.round(metrics.disk.total / (1024 * 1024 * 1024)), // Convert to GB
        percentage: metrics.disk.percentage.toFixed(1),
      },
      memoryUsage: {
        used: (metrics.memory.used / (1024 * 1024 * 1024)).toFixed(1), // Convert to GB
        total: Math.round(metrics.memory.total / (1024 * 1024 * 1024)), // Convert to GB
        percentage: metrics.memory.percentage.toFixed(1),
      },
      cpuLoad: metrics.cpu.usage.toFixed(1),
      uptime: Math.floor(metrics.uptime), // Uptime in seconds
    };
    
    return NextResponse.json(formattedMetrics);
  } catch (error) {
    console.error('Failed to fetch system metrics:', error);
    
    // Return mock data as fallback
    return NextResponse.json({
      diskUsage: {
        used: 1248,
        total: 2000,
        percentage: '62.4',
      },
      memoryUsage: {
        used: '6.2',
        total: 32,
        percentage: '19.4',
      },
      cpuLoad: '18.5',
      uptime: 273600, // 76 hours in seconds
    }, { status: 503 }); // Service unavailable but with fallback data
  }
}