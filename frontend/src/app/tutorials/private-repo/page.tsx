import Link from "next/link";
import { TutorialContent } from "@/components/TutorialContent";

export default function PublicPrivateRepoTutorialPage() {
  return (
    <div className="min-h-screen bg-white">
      <header className="border-b bg-navy-950">
        <div className="max-w-6xl mx-auto px-6 py-4 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <img src="/logo-square.png" alt="" className="h-8 w-8 rounded" />
            <Link href="/" className="text-xl font-bold text-white">SWAMP</Link>
            <span className="text-xs bg-brand-100 text-brand-700 px-2 py-0.5 rounded font-medium">beta</span>
          </div>
          <div className="flex items-center gap-3">
            <Link href="/" className="text-sm text-gray-300 hover:text-white">Home</Link>
            <Link href="/login" className="text-sm bg-brand-600 text-white px-4 py-2 rounded hover:bg-brand-700 transition">
              Sign In
            </Link>
          </div>
        </div>
      </header>

      <section className="max-w-6xl mx-auto px-6 py-10 lg:py-14">
        <div className="mb-8">
          <p className="text-xs uppercase tracking-wide text-brand-700 font-semibold mb-2">Public Tutorial</p>
          <h1 className="text-3xl lg:text-4xl font-bold text-gray-900">Analyzing a Private GitHub Repository</h1>
          <p className="mt-3 text-base text-gray-600 max-w-3xl">
            Connect GitHub access and install the SWAMP GitHub App before adding a private repository to a project.
          </p>
          <div className="mt-4">
            <Link href="/login" className="text-sm text-brand-700 hover:text-brand-900 hover:underline">
              Sign in and continue in-app →
            </Link>
          </div>
        </div>

        <TutorialContent tutorialPath="private-repo" />
      </section>

      <footer className="bg-navy-950 border-t border-navy-800 mt-12">
        <div className="max-w-6xl mx-auto px-6 py-6 flex items-center justify-between">
          <p className="text-sm text-gray-400">SWAMP — Software Assurance Marketplace</p>
          <Link href="/login" className="text-sm text-brand-400 hover:text-brand-300 hover:underline">
            Sign In
          </Link>
        </div>
      </footer>
    </div>
  );
}