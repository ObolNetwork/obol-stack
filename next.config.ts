import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Set Turbopack root to silence lockfile warning
  turbopack: {
    root: process.cwd(),
  },
  
  webpack: (config) => {
    config.resolve = config.resolve || {};
    config.resolve.alias = {
      ...(config.resolve.alias || {}),
      "@react-native-async-storage/async-storage": false,
    };
    return config;
  },

  async rewrites() {
    return [
      {
        source: "/monitoring/:path*",
        destination: "http://monitoring.obol.stack/:path*",
      },
    ];
  },

  async headers() {
    return [
      {
        source: "/(.*)",
        headers: [
          { key: "X-Frame-Options", value: "DENY" },
          { key: "X-Content-Type-Options", value: "nosniff" },
        ],
      },
    ];
  },

  output: "standalone",

  // Externalize server-only packages
  serverExternalPackages: ['pino', 'thread-stream'],
};

export default nextConfig;
