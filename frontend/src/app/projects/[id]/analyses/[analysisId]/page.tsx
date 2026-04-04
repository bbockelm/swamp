import AnalysisDetailClient from './AnalysisDetailClient';

// Required for Next.js static export (output: 'export').
// The Go server's SPA fallback serves this for any matching route;
// the client-side router picks up the actual IDs from the URL.
export async function generateStaticParams() {
  return [{ id: '_', analysisId: '_' }];
}

export default function AnalysisDetailPage() {
  return <AnalysisDetailClient />;
}
