import type { NextConfig } from "next";
import { execSync } from "child_process";

function git(cmd: string): string {
  try {
    return execSync(cmd, { stdio: ["pipe", "pipe", "ignore"] })
      .toString()
      .trim();
  } catch {
    return "";
  }
}

const commitSha =
  git("git rev-parse --short HEAD") ||
  process.env.NEXT_PUBLIC_COMMIT_SHA ||
  process.env.NEXT_PUBLIC_GITCOMMIT ||
  "local";

const tagVersion =
  git("git describe --tags --abbrev=0") ||
  process.env.NEXT_PUBLIC_TAG_VERSION ||
  process.env.NEXT_PUBLIC_VERSION ||
  "local";

const buildTime =
  process.env.NEXT_PUBLIC_BUILD_TIME ||
  process.env.NEXT_PUBLIC_BUILDTIME ||
  new Date().toISOString();

const securityHeaders = [
  { key: "X-Frame-Options", value: "DENY" },
  { key: "X-Content-Type-Options", value: "nosniff" },
];

const noStoreHeaders = [
  {
    key: "Cache-Control",
    value: "no-store, no-cache, max-age=0, must-revalidate",
  },
  { key: "Pragma", value: "no-cache" },
  { key: "Expires", value: "0" },
];

const nextConfig: NextConfig = {
  allowedDevOrigins: ['obol.stack', 'monitoring.obol.stack'],
  env: {
    NEXT_PUBLIC_COMMIT_SHA: commitSha,
    NEXT_PUBLIC_TAG_VERSION: tagVersion,
    NEXT_PUBLIC_BUILD_TIME: buildTime,
  },
  // Set Turbopack root to silence lockfile warning
  turbopack: {
    root: process.cwd(),
  },

  async headers() {
    return [
      {
        source: "/(.*)",
        headers: securityHeaders,
      },
      {
        source: "/",
        headers: noStoreHeaders,
      },
      {
        source: "/marketplace",
        headers: noStoreHeaders,
      },
      {
        source: "/marketplace/:path*",
        headers: noStoreHeaders,
      },
    ];
  },

  output: "standalone",
};

export default nextConfig;
