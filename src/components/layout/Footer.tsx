import React from 'react';
import { Download, FileText, ExternalLink } from 'lucide-react';

export const Footer: React.FC = () => {
  const handleExportDiagnostics = () => {
    // This would be replaced with real diagnostic data export
    const diagnostics = {
      timestamp: new Date().toISOString(),
      // Add diagnostic data here
    };
    navigator.clipboard.writeText(JSON.stringify(diagnostics, null, 2));
  };

  return (
    <footer className="border-t border-stroke px-6 py-4 mt-8">
      <div className="flex items-center justify-between">
        {/* Minimal Ghost Buttons - Bottom Left */}
        <div className="flex items-center gap-2">
          <button
            onClick={handleExportDiagnostics}
            className="flex items-center gap-2 px-3 py-1.5 text-xs text-gray-400 hover:text-white transition-colors hover:bg-muted/50 rounded-lg"
          >
            <Download className="w-3 h-3" />
            Export Diagnostics
          </button>
          <button
            onClick={() =>
              window.open('http://monitoring.obol.stack', '_blank')
            }
            className="flex items-center gap-2 px-3 py-1.5 text-xs text-gray-400 hover:text-white transition-colors hover:bg-muted/50 rounded-lg"
          >
            <FileText className="w-3 h-3" />
            View Logs
          </button>
        </div>

        {/* Minimal Links - Bottom Right */}
        <div className="flex items-center gap-3">
          <button
            onClick={() =>
              window.open('https://github.com/ObolNetwork', '_blank')
            }
            className="flex items-center gap-1 px-2 py-1 text-xs text-gray-500 hover:text-gray-300 transition-colors"
          >
            <ExternalLink className="w-3 h-3" />
            GitHub
          </button>
          <button
            onClick={() => window.open('https://discord.gg/obol', '_blank')}
            className="flex items-center gap-1 px-2 py-1 text-xs text-gray-500 hover:text-gray-300 transition-colors"
          >
            <ExternalLink className="w-3 h-3" />
            Discord
          </button>
        </div>
      </div>
    </footer>
  );
};
