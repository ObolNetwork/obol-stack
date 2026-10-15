import { NextResponse } from 'next/server';
import { AgentClient } from '@/lib/services/agents';
import {
  AGENT_REGISTRATION_PATH,
  publicResourceUrl,
} from '@/lib/services/public-storefront';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * GET /api/agents/registration
 * Fetches the ERC-8004 agent registration document from the public storefront
 * URL. Returns null only when the document is not published; upstream failures
 * are surfaced as errors so the UI does not mislabel connectivity as absence.
 */
export async function GET() {
  try {
    const agentClient = new AgentClient();
    const tunnelURL = await agentClient.getTunnelURL();
    const url = publicResourceUrl(tunnelURL, AGENT_REGISTRATION_PATH);
    const response = await fetch(url, {
      next: { revalidate: 30 },
      signal: AbortSignal.timeout(5000),
    });

    if (response.status === 404) {
      return NextResponse.json({ registration: null, tunnelURL });
    }

    if (!response.ok) {
      return NextResponse.json(
        {
          registration: null,
          tunnelURL,
          error: `registration endpoint returned HTTP ${response.status}`,
        },
        { status: 502 }
      );
    }

    const registration = await response.json();
    return NextResponse.json({ registration, tunnelURL });
  } catch (error) {
    console.error('[AgentRegistration] Error fetching registration:', error);
    return NextResponse.json(
      {
        registration: null,
        error:
          error instanceof Error
            ? error.message
            : 'failed to fetch registration document',
      },
      { status: 502 }
    );
  }
}
