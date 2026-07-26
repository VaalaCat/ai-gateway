import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";

const withNextIntl = createNextIntlPlugin("./src/i18n/request.ts");
const apiOrigin = process.env.AIGW_API_ORIGIN ?? "http://localhost:8140";
const requestedDistDir = process.env.AIGW_E2E_DIST_DIR;
const distDir = requestedDistDir === ".next-e2e-normal" || requestedDistDir === ".next-e2e-degraded"
  ? requestedDistDir
  : "dist";

const nextConfig: NextConfig = {
  ...(process.env.AIGW_E2E_SERVER === "1" ? {} : { output: "export" as const }),
  distDir,
  trailingSlash: true,
  images: { unoptimized: true },
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${apiOrigin}/api/:path*`,
      },
      {
        source: "/v1/:path*",
        destination: `${apiOrigin}/v1/:path*`,
      },
    ];
  },
};

export default withNextIntl(nextConfig);
