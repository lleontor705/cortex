import type { NextConfig } from "next";

// The embedded web UI is a static export shipped inside the cortex binary
// (internal/web/dist). Next.js generates a random buildId per build, which
// makes index.html non-reproducible across machines and breaks the release
// pipeline's committed-artifact freshness check (release.yml runs
// `make web-build` and fails if the tracked index.html changes). A constant
// buildId is safe here: there is no external deployment, CDN cache, or
// incremental cache keyed by the build id.
const EMBEDDED_WEB_BUILD_ID = "cortex-embedded-web";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  output: "export",
  trailingSlash: true,
  generateBuildId: () => Promise.resolve(EMBEDDED_WEB_BUILD_ID),
};

export default nextConfig;
