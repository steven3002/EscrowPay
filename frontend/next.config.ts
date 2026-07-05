import type { NextConfig } from "next";

// The browser talks to the Go API through a same-origin proxy, so no CORS
// configuration is needed on the backend and the demo works from one host. The
// backend origin is overridable for non-local deployments.
const backendOrigin = process.env.BACKEND_ORIGIN ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  // The route indicator overlaps the fixed nav in this layout; errors still surface.
  devIndicators: false,
  async rewrites() {
    return [
      { source: "/api/:path*", destination: `${backendOrigin}/api/:path*` },
      { source: "/healthz", destination: `${backendOrigin}/healthz` },
    ];
  },
};

export default nextConfig;
