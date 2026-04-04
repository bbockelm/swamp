import { Suspense } from 'react';
import ProjectDetailClient from './ProjectDetailClient';

// Required for Next.js static export (output: 'export').
// We generate a single placeholder page; the Go server's SPA fallback
// serves this HTML for any /projects/<uuid> route and the client-side
// router picks up the actual ID from the URL.
export async function generateStaticParams() {
  return [{ id: '_' }];
}

export default function ProjectDetailPage() {
  return (
    <Suspense fallback={<p>Loading...</p>}>
      <ProjectDetailClient />
    </Suspense>
  );
}
