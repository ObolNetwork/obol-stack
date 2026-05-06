import { NextResponse } from 'next/server';
import { AgentClient } from '@/lib/services/agents';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * GET /api/agents/registration
 * Fetches the ERC-8004 agent registration document from the tunnel URL.
 * Returns null fields when no tunnel is provisioned or registration is not yet published.
 */
export async function GET() {
  try {
    const agentClient = new AgentClient();
    const tunnelURL = await agentClient.getTunnelURL();

    if (!tunnelURL) {
      return NextResponse.json({ registration: null });
    }

    const url = `${tunnelURL.replace(/\/$/, '')}/.well-known/agent-registration.json`;
    const response = await fetch(url, {
      next: { revalidate: 30 },
      signal: AbortSignal.timeout(5000),
    });

    if (!response.ok) {
      return NextResponse.json({ registration: null });
    }

    const registration = await response.json();
    return NextResponse.json({ registration, tunnelURL });
  } catch (error) {
    console.error('[AgentRegistration] Error fetching registration:', error);
    return NextResponse.json({ registration: null });
  }
}
