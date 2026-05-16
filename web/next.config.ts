import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";

const withNextIntl = createNextIntlPlugin("./src/i18n/request.ts");

const nextConfig: NextConfig = {
  output: "export",
  distDir: "dist",
  trailingSlash: true,
  images: { unoptimized: true },
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: "http://localhost:8140/api/:path*",
      },
      {
        source: "/v1/:path*",
        destination: "http://localhost:8140/v1/:path*",
      },
    ];
  },
};

export default withNextIntl(nextConfig);
