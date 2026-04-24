"use client";

import { useEffect, useState } from "react";
import { RenderedMarkdown } from "@/components/MarkdownReport";

type TutorialPayload = {
  title: string;
  markdown: string;
  image_base_path: string;
};

type TutorialContentProps = {
  tutorialPath: string;
};

type TutorialState = {
  loadedPath: string | null;
  payload: TutorialPayload | null;
  error: string;
};

export function TutorialContent({ tutorialPath }: TutorialContentProps) {
  const [state, setState] = useState<TutorialState>({ loadedPath: null, payload: null, error: "" });

  useEffect(() => {
    let cancelled = false;

    fetch(`/api/v1/tutorials/${tutorialPath}`)
      .then((r) => {
        if (!r.ok) {
          throw new Error("Failed to load tutorial content");
        }
        return r.json();
      })
      .then((data: TutorialPayload) => {
        if (!cancelled) {
          setState({ loadedPath: tutorialPath, payload: data, error: "" });
        }
      })
      .catch((err: Error) => {
        if (!cancelled) {
          setState({ loadedPath: tutorialPath, payload: null, error: err.message || "Failed to load tutorial content" });
        }
      });

    return () => {
      cancelled = true;
    };
  }, [tutorialPath]);

  if (state.loadedPath !== tutorialPath) {
    return <p className="text-sm text-gray-500">Loading tutorial...</p>;
  }

  if (state.error) {
    return <p className="text-sm text-red-600">{state.error}</p>;
  }

  const payload = state.payload;
  if (!payload) {
    return <p className="text-sm text-gray-500">Loading tutorial...</p>;
  }

  // The surrounding page already renders the tutorial title, so strip the
  // leading H1 from the markdown to avoid showing it twice.
  const markdown = stripLeadingH1(payload.markdown);

  return (
    <article className="rounded-xl border border-gray-200 bg-white p-5 sm:p-8 lg:p-10 shadow-sm overflow-hidden">
      <div className="mx-auto max-w-3xl text-base text-gray-700">
        <RenderedMarkdown content={markdown} imageBasePath={payload.image_base_path} />
      </div>
    </article>
  );
}

function stripLeadingH1(markdown: string): string {
  const lines = markdown.split("\n");
  let i = 0;
  // Skip any leading blank lines.
  while (i < lines.length && lines[i].trim() === "") i++;
  if (i < lines.length && lines[i].startsWith("# ")) {
    // Drop the H1 line and any blank line immediately after it.
    i++;
    while (i < lines.length && lines[i].trim() === "") i++;
    return lines.slice(i).join("\n");
  }
  return markdown;
}
