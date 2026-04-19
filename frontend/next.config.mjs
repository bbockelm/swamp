/** @type {import('next').NextConfig} */
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const nextConfig = {
  // Static export: produces plain HTML/JS/CSS in out/ for embedding into the Go binary.
  // Only enable for production builds so `next dev` can serve dynamic routes normally.
  ...(process.env.NODE_ENV === 'production' ? { output: 'export' } : {}),

  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: "http://localhost:8080/api/:path*",
      },
      {
        source: "/healthz",
        destination: "http://localhost:8080/healthz",
      },
      {
        source: "/ws/:path*",
        destination: "http://localhost:8080/ws/:path*",
      },
      {
        source: "/.well-known/:path*",
        destination: "http://localhost:8080/.well-known/:path*",
      },
      {
        source: "/oauth/:path*",
        destination: "http://localhost:8080/oauth/:path*",
      },
      {
        source: "/mcp",
        destination: "http://localhost:8080/mcp",
      },
      {
        source: "/aup",
        destination: "http://localhost:8080/aup",
      },
      {
        source: "/aup-v:path(.*)",
        destination: "http://localhost:8080/aup-v:path",
      },
    ];
  },

  webpack(config) {
    config.resolve.alias["@pricing"] = path.resolve(__dirname, "..", "pricing");
    return config;
  },
};

export default nextConfig;
