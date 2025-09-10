import { NextResponse } from 'next/server';
import { ERPCClient } from '@/lib/services/erpc';
import { config } from '@/lib/config/environment';

export async function GET(request: Request) {
  const { searchParams } = new URL(request.url);
  const network = searchParams.get('network') || config.defaultNetwork;
  
  try {
    const erpc = new ERPCClient(config.erpcUrl);
    const metrics = await erpc.getNetworkMetrics(network);
    
    // Calculate finalized blocks (mock for now - needs beacon API)
    const finalizedBlocks = Math.max(0, metrics.blockHeight - 3);
    
    // Calculate participation rate (mock for now - needs validator metrics)
    const participationRate = metrics.synced ? 97.6 : 85.2;
    
    // Format the response
    const formattedMetrics = {
      network,
      status: metrics.synced ? 'synced' : 'syncing',
      blockHeight: metrics.blockHeight,
      finalizedBlocks,
      participationRate,
      syncProgress: metrics.syncProgress,
      metrics: metrics.metrics,
      // Distance to justified is typically 0 when synced
      distanceToJustified: metrics.synced ? 0 : Math.max(0, 10 - Math.floor(metrics.syncProgress / 10)),
    };
    
    return NextResponse.json(formattedMetrics);
  } catch (error) {
    console.error(`Failed to fetch Ethereum metrics for ${network}:`, error);
    
    // Return mock data as fallback
    const mockData = {
      mainnet: {
        network: 'mainnet',
        status: 'synced',
        blockHeight: 18972341,
        finalizedBlocks: 254170,
        participationRate: 97.6,
        syncProgress: 100,
        distanceToJustified: 0,
      },
      hoodi: {
        network: 'hoodi',
        status: 'synced',
        blockHeight: 1183092,
        finalizedBlocks: 1183089,
        participationRate: 96.8,
        syncProgress: 100,
        distanceToJustified: 0,
      },
    };
    
    return NextResponse.json(
      mockData[network as keyof typeof mockData] || mockData.hoodi,
      { status: 503 }
    );
  }
}