import type { NextConfig } from "next";

const servicesURL =
  process.env.SERVICES_URL ?? "http://obol-skill-md.x402.svc:8080";

const nextConfig: NextConfig = {
  output: "standalone",
  // Local dev has no Traefik in front — proxy catalog JSON through Next so the
  // client-side refresh in ServicesList hits the cluster via SERVICES_URL.
  async rewrites() {
    return [
      {
        source: "/api/services.json",
        destination: `${servicesURL}/api/services.json`,
      },
    ];
  },
};

export default nextConfig;
