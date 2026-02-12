import { NextResponse } from 'next/server';
import { OpenClawClient } from '@/lib/services/openclaw';

export async function GET() {
  try {
    const client = new OpenClawClient();
    const instances = await client.listInstances();
    return NextResponse.json({ instances });
  } catch (error) {
    console.error('[OpenClaw List API] Error:', error);
    return NextResponse.json(
      { error: 'Failed to list OpenClaw instances' },
      { status: 500 }
    );
  }
}
