import { NextResponse } from 'next/server';
import { AgentClient } from '@/lib/services/agents';

export async function GET() {
  try {
    const client = new AgentClient();
    const instances = await client.listInstances();
    return NextResponse.json({ instances });
  } catch (error) {
    console.error('[Agent List API] Error:', error);
    return NextResponse.json(
      { error: 'Failed to list agent instances' },
      { status: 500 }
    );
  }
}
