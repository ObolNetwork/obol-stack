import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Configure for custom domain
  async rewrites() {
    return [
      {
        source: '/monitoring/:path*',
        destination: 'http://monitoring.obol.stack/:path*',
      },
    ];
  },
  
  // Configure hostname
  async headers() {
    return [
      {
        source: '/(.*)',
        headers: [
          {
            key: 'X-Frame-Options',
            value: 'DENY',
          },
          {
            key: 'X-Content-Type-Options',
            value: 'nosniff',
          },
        ],
      },
    ];
  },
  
  // Output configuration for standalone deployment
  output: 'standalone',
};

export default nextConfig;
