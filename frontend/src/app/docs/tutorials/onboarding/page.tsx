import Link from "next/link";
import { TutorialContent } from "@/components/TutorialContent";

export default function DocsOnboardingTutorialPage() {
  return (
    <div className="mx-auto w-full max-w-6xl">
      <div className="mb-6">
        <p className="text-xs uppercase tracking-wide text-brand-700 font-semibold mb-2">Tutorial</p>
        <h1 className="text-2xl sm:text-3xl font-bold text-gray-900">Getting Started with SWAMP</h1>
        <p className="mt-2 text-sm text-gray-600 max-w-3xl">
          End-to-end onboarding walkthrough in the same app experience as the rest of SWAMP.
        </p>
        <div className="mt-4 flex items-center gap-4 text-sm">
          <Link href="/docs" className="text-brand-700 hover:text-brand-900 hover:underline">
            ← Back to Documentation
          </Link>
          <Link href="/tutorials/onboarding" className="text-gray-600 hover:text-gray-900 hover:underline">
            Open Public Version
          </Link>
        </div>
      </div>

      <TutorialContent />
    </div>
  );
}
