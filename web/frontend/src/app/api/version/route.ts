import { NextResponse } from 'next/server';
import { OpenClawClient } from '@/lib/services/openclaw';
import { config } from '@/lib/config/environment';

/**
 * GET /api/version
 * Returns the running Obol CLI/stack version (from obol-stack-config or
 * OBOL_STACK_VERSION) plus the frontend image/build tag baked at build time.
 */
export async function GET() {
  let obolVersion: string | null =
    process.env.OBOL_STACK_VERSION?.trim() || null;

  if (!obolVersion) {
    try {
      const client = new OpenClawClient();
      obolVersion = await client.getObolStackVersion();
    } catch (error) {
      console.error('[Version] Error fetching Obol stack version:', error);
    }
  }

  return NextResponse.json({
    obol: { version: obolVersion },
    frontend: {
      tag: config.tagVersion,
      commit: config.commitSha,
      buildTime: config.buildTime || null,
    },
  });
}
