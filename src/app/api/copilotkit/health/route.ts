import { NextResponse } from 'next/server';

// Get agent URL from environment with fallback to local development server
const agentUrl = process.env.ADK_AGENT_URL || 'http://127.0.0.1:8000/';

/**
 * Health check endpoint to verify if the ADK agent is available
 * Attempts to connect to the agent URL to determine availability
 */
export async function GET() {
  try {
    // Try to make a simple request to the agent to check if it's available
    // Use a timeout to avoid hanging
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 3000);

    try {
      // Attempt to fetch from the agent URL
      // Any HTTP response (even 404/405) indicates the server is reachable
      const response = await fetch(agentUrl, {
        method: 'GET',
        headers: {
          Accept: 'application/json',
        },
        signal: controller.signal,
      });

      clearTimeout(timeoutId);

      // Any response (even 404/405/500) indicates the server is reachable
      // Only connection failures indicate unavailability
      const isAvailable = response.status > 0;

      return NextResponse.json(
        {
          available: isAvailable,
          agentUrl,
        },
        { status: 200 }
      );
    } catch (fetchError) {
      clearTimeout(timeoutId);
      throw fetchError;
    }
  } catch (error) {
    // Connection errors mean the agent is not available
    const errorMessage =
      error instanceof Error ? error.message : 'Unknown error occurred';

    const isConnectionError =
      errorMessage.includes('ECONNREFUSED') ||
      errorMessage.includes('ENOTFOUND') ||
      errorMessage.includes('fetch failed') ||
      errorMessage.includes('network') ||
      errorMessage.includes('timeout') ||
      errorMessage.includes('aborted') ||
      errorMessage.includes('Failed to fetch') ||
      errorMessage === 'The operation was aborted.' ||
      errorMessage === 'This operation was aborted';

    return NextResponse.json(
      {
        available: false,
        agentUrl,
        error: isConnectionError ? 'Connection failed' : errorMessage,
      },
      { status: 200 } // Return 200 so frontend can handle the available: false
    );
  }
}
