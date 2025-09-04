import React from 'react';
import { Download, FileText, Github, MessageCircle } from 'lucide-react';

interface FooterBarProps {
  className?: string;
}

export const FooterBar: React.FC<FooterBarProps> = ({ className = '' }) => {
  return (
    <footer
      className={`flex items-center justify-between text-sm text-zinc-400 ${className}`}
    >
      <div className="flex items-center gap-4">
        <button
          className="flex items-center gap-2 hover:text-zinc-200 transition-colors focus:outline-none focus:ring-2 focus:ring-emerald-400/40 rounded"
          onClick={() => {
            // TODO: Implement diagnostics export
            console.log('Export diagnostics clicked');
          }}
        >
          <Download className="w-4 h-4" />
          Export Diagnostics
        </button>
        <button
          className="flex items-center gap-2 hover:text-zinc-200 transition-colors focus:outline-none focus:ring-2 focus:ring-emerald-400/40 rounded"
          onClick={() => {
            // TODO: Implement view logs
            console.log('View logs clicked');
          }}
        >
          <FileText className="w-4 h-4" />
          View Logs
        </button>
      </div>

      <div className="flex items-center gap-4">
        <a
          href="https://github.com/ObolNetwork"
          target="_blank"
          rel="noopener noreferrer"
          className="flex items-center gap-2 hover:text-zinc-200 transition-colors focus:outline-none focus:ring-2 focus:ring-emerald-400/40 rounded"
        >
          <Github className="w-4 h-4" />
          GitHub
        </a>
        <a
          href="https://discord.gg/obol"
          target="_blank"
          rel="noopener noreferrer"
          className="flex items-center gap-2 hover:text-zinc-200 transition-colors focus:outline-none focus:ring-2 focus:ring-emerald-400/40 rounded"
        >
          <MessageCircle className="w-4 h-4" />
          Discord
        </a>
      </div>
    </footer>
  );
};
