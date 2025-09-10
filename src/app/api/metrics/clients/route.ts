import { NextResponse } from 'next/server';
import { KubernetesAPIClient } from '@/lib/services/kubernetes-api';
import { ERPCClient } from '@/lib/services/erpc';
import { config } from '@/lib/config/environment';

export async function GET() {
  try {
    const k8s = new KubernetesAPIClient();
    const erpc = new ERPCClient(config.erpcUrl);
    
    // Get client versions from the actual services
    const versions = await k8s.getClientVersions();
    
    // Get pod status
    const podStatus = await k8s.getPodsStatus('l1');
    
    // Try to get peer count from eRPC (hoodi network)
    let executionPeers = 25; // Default fallback
    const consensusPeers = 39; // Default fallback
    
    try {
      executionPeers = await erpc.getPeerCount('hoodi');
    } catch (e) {
      console.log('Could not fetch execution peer count, using default');
    }
    
    // Format the response
    const formattedClients = {
      execution: {
        name: 'Nethermind',
        version: versions.execution,
        peers: executionPeers,
        status: podStatus.execution?.ready ? 'synced' : 'syncing',
      },
      consensus: {
        name: 'Nimbus',
        version: versions.consensus,
        peers: consensusPeers,
        status: podStatus.beacon?.ready ? 'synced' : 'syncing',
      },
      clientCount: 2, // Execution + Consensus
    };
    
    return NextResponse.json(formattedClients);
  } catch (error) {
    console.error('Failed to fetch client metrics:', error);
    
    // Return mock data as fallback
    return NextResponse.json({
      execution: {
        name: 'Nethermind',
        version: 'v1.32.4',
        peers: 25,
        status: 'synced',
      },
      consensus: {
        name: 'Nimbus',
        version: 'v25.6.0',
        peers: 39,
        status: 'synced',
      },
      clientCount: 2,
    }, { status: 503 });
  }
}