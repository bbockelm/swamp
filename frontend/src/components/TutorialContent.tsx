"use client";

import { useEffect, useState } from "react";
import { RenderedMarkdown } from "@/components/MarkdownReport";

type TutorialPayload = {
  title: string;
  markdown: string;
  image_base_path: string;
};

export function TutorialContent() {
  const [payload, setPayload] = useState<TutorialPayload | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    fetch("/api/v1/tutorials/onboarding")
      .then((r) => {
        if (!r.ok) {
          throw new Error("Failed to load tutorial content");
        }
        return r.json();
      })
      .then((data: TutorialPayload) => {
        setPayload(data);
      })
      .catch((err: Error) => {
        setError(err.message || "Failed to load tutorial content");
      });
  }, []);

  if (error) {
    return <p className="text-sm text-red-600">{error}</p>;
  }

  if (!payload) {
    return <p className="text-sm text-gray-500">Loading tutorial...</p>;
  }

  return (
    <article className="rounded-xl border border-gray-200 bg-white p-4 sm:p-6 lg:p-8 shadow-sm overflow-hidden">
      <div className="prose prose-sm sm:prose-base max-w-none text-gray-700">
        <RenderedMarkdown content={payload.markdown} imageBasePath={payload.image_base_path} />
      </div>
    </article>
  );
}
