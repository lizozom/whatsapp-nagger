import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  serverExternalPackages: ["better-sqlite3"],
  rewrites: async () => [
    // Proxy Go bot endpoints (ingest, health, auth) to localhost:8080
    { source: "/ingest/:path*", destination: "http://localhost:8080/ingest/:path*" },
    { source: "/healthz", destination: "http://localhost:8080/healthz" },
    { source: "/health", destination: "http://localhost:8080/health" },
    { source: "/pair", destination: "http://localhost:8080/pair" },
    // Auth routes are handled by Next.js route handlers (to set cookies), not proxied.
  ],
};

export default nextConfig;
