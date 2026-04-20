"use client";

import { useEffect } from "react";

export default function GitHubLinkedPage() {
  const hasOpener = typeof window !== "undefined" && !!window.opener;

  useEffect(() => {
    // Notify the opener (if any) and close this popup.
    if (window.opener) {
      try {
        window.opener.postMessage({ type: "github-linked" }, window.location.origin);
      } catch {
        // Cross-origin or closed opener — ignore.
      }
      window.close();
    }
  }, []);

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-gray-900">
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-lg p-8 max-w-md text-center">
        <div className="text-4xl mb-4">✅</div>
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white mb-2">
          GitHub Account Linked
        </h1>
        <p className="text-gray-600 dark:text-gray-400">
          {hasOpener
            ? "This window will close automatically..."
            : "Your GitHub account has been linked. You can close this window and return to SWAMP."}
        </p>
      </div>
    </div>
  );
}
