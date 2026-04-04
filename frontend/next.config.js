/** @type {import('next').NextConfig} */
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
    ];
  },
};

module.exports = nextConfig;
