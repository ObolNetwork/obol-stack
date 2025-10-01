'use client';

import { CopilotSidebar } from '@copilotkit/react-ui';
import { CopilotKitCSSProperties } from '@copilotkit/react-ui';

export default function AgentPage() {
  return (
    <main
      style={
        {
          '--copilot-kit-primary-color': '#2FE4AB',
        } as CopilotKitCSSProperties
      }
      className="h-screen w-screen flex flex-col bg-gradient-to-br from-[#162A40] via-[#1a3350] to-[#162A40]"
    >
      <div className="flex-1 flex flex-col items-center justify-center p-8">
        <div className="max-w-4xl w-full bg-white/5 backdrop-blur-md rounded-2xl p-8 border border-white/10">
          <div className="text-center space-y-4">
            <h1 className="text-5xl font-bold text-white mb-2">
              Obol ADK Agent
            </h1>
            <p className="text-xl text-[#2FE4AB] font-medium">
              Powered by Google ADK + AG-UI Protocol
            </p>
            <div className="mt-8 space-y-2 text-gray-300">
              <p className="text-lg">
                Welcome to the Obol Stack Agent Interface
              </p>
              <p className="text-sm">
                Open the sidebar to interact with your AI agent →
              </p>
            </div>
          </div>

          <div className="mt-12 grid grid-cols-1 md:grid-cols-3 gap-4">
            <div className="bg-white/5 p-4 rounded-lg border border-white/10">
              <div className="text-[#2FE4AB] text-2xl mb-2">🤖</div>
              <h3 className="text-white font-semibold mb-1">Smart Agent</h3>
              <p className="text-gray-400 text-sm">
                Powered by Google ADK with planning and reasoning capabilities
              </p>
            </div>

            <div className="bg-white/5 p-4 rounded-lg border border-white/10">
              <div className="text-[#9167E4] text-2xl mb-2">🔄</div>
              <h3 className="text-white font-semibold mb-1">Real-time Sync</h3>
              <p className="text-gray-400 text-sm">
                AG-UI protocol enables seamless frontend-backend communication
              </p>
            </div>

            <div className="bg-white/5 p-4 rounded-lg border border-white/10">
              <div className="text-[#2FE4AB] text-2xl mb-2">🎨</div>
              <h3 className="text-white font-semibold mb-1">Rich UI</h3>
              <p className="text-gray-400 text-sm">
                Interactive components with generative UI and shared state
              </p>
            </div>
          </div>
        </div>
      </div>

      <CopilotSidebar
        defaultOpen={true}
        clickOutsideToClose={false}
        labels={{
          title: 'Obol ADK Agent',
          initial:
            "👋 Hi! I'm your Obol ADK Agent.\n\nI'm powered by Google's Agent Development Kit and connected via the AG-UI protocol.\n\nHow can I help you with your Obol Stack today?",
        }}
      />
    </main>
  );
}
