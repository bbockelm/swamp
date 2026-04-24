import Link from "next/link";
import { TutorialContent } from "@/components/TutorialContent";

export default function DocsPrivateRepoTutorialPage() {
  return (
    <div className="mx-auto w-full max-w-6xl">
      <div className="mb-6">
        <p className="text-xs uppercase tracking-wide text-brand-700 font-semibold mb-2">Tutorial</p>
        <h1 className="text-2xl sm:text-3xl font-bold text-gray-900">Analyzing a Private GitHub Repository</h1>
        <p className="mt-2 text-sm text-gray-600 max-w-3xl">
          Link GitHub access, install the SWAMP GitHub App, and analyze repositories that require authenticated cloning.
        </p>
        <div className="mt-4 flex items-center gap-4 text-sm">
          <Link href="/docs" className="text-brand-700 hover:text-brand-900 hover:underline">
            ← Back to Documentation
          </Link>
        </div>
      </div>

      <TutorialContent tutorialPath="private-repo" />
    </div>
  );
}