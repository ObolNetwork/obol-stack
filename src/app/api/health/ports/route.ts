import { NextResponse } from 'next/server';
import { KubernetesAPIClient } from '@/lib/services/kubernetes-api';

export async function GET() {
  try {
    const k8s = new KubernetesAPIClient();
    const portStatus = await k8s.checkPorts();
    
    // Format the response to match the expected dashboard format
    const formattedPorts = portStatus.map(p => ({
      port: p.port,
      name: p.name,
      status: p.open ? 'Open' : 'Closed',
      service: p.service,
    }));
    
    return NextResponse.json({
      ports: formattedPorts,
      summary: {
        total: portStatus.length,
        open: portStatus.filter(p => p.open).length,
        closed: portStatus.filter(p => !p.open).length,
      },
    });
  } catch (error) {
    console.error('Failed to check port status:', error);
    
    // Return mock data as fallback
    return NextResponse.json({
      ports: [
        { port: 30303, name: 'P2P', status: 'Open', service: 'l1-full-node-execution' },
        { port: 8545, name: 'JSON-RPC', status: 'Closed', service: 'l1-full-node-execution' },
        { port: 9090, name: 'Prometheus', status: 'Closed', service: 'monitoring-prometheus' },
        { port: 5052, name: 'Beacon API', status: 'Open', service: 'l1-full-node-beacon' },
        { port: 80, name: 'HTTP', status: 'Open', service: 'ingress-nginx' },
        { port: 443, name: 'HTTPS', status: 'Open', service: 'ingress-nginx' },
      ],
      summary: {
        total: 6,
        open: 4,
        closed: 2,
      },
    }, { status: 503 });
  }
}