import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { NextResponse } from 'next/server';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * Dev-only mock of the controller-published `/api/services.json` catalog.
 *
 * Returns the fixture under `src/lib/marketplace/__fixtures__/services.json`
 * so `pnpm dev` can render the marketplace without a running cluster. Gated
 * to NODE_ENV !== 'production' so a prod build never exposes this.
 *
 * Point the marketplace at it with:
 *
 *   MARKETPLACE_CATALOG_URL=http://localhost:3000/api/__dev/services-catalog
 */
export async function GET() {
  if (process.env.NODE_ENV === 'production') {
    return NextResponse.json({ error: 'dev only' }, { status: 404 });
  }
  const path = resolve(
    process.cwd(),
    'src/lib/marketplace/__fixtures__/services.json'
  );
  const body = await readFile(path, 'utf8');
  return new NextResponse(body, {
    headers: { 'content-type': 'application/json' },
  });
}
