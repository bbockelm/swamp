"use client";

import Link from "next/link";

interface DocLinkCardProps {
  title: string;
  description: string;
  href: string;
  external?: boolean;
  badge?: string;
}

function DocLinkCard({ title, description, href, external, badge }: DocLinkCardProps) {
  const inner = (
    <div className="bg-white rounded-lg border p-5 hover:border-brand-400 hover:shadow-sm transition group flex flex-col gap-2 h-full">
      <div className="flex items-start justify-between gap-2">
        <h3 className="font-semibold text-gray-900 group-hover:text-brand-700 transition">{title}</h3>
        {badge && (
          <span className="flex-shrink-0 text-xs bg-brand-100 text-brand-700 px-2 py-0.5 rounded font-medium">
            {badge}
          </span>
        )}
      </div>
      <p className="text-sm text-gray-600 leading-relaxed flex-1">{description}</p>
      <span className="text-sm text-brand-600 font-medium mt-1">
        {external ? "Open ↗" : "Read →"}
      </span>
    </div>
  );

  if (external) {
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" className="block h-full">
        {inner}
      </a>
    );
  }
  return <Link href={href} className="block h-full">{inner}</Link>;
}

interface SectionProps {
  title: string;
  children: React.ReactNode;
}

function Section({ title, children }: SectionProps) {
  return (
    <div>
      <h2 className="text-sm font-semibold text-gray-500 uppercase tracking-wide mb-4">{title}</h2>
      <div className="grid md:grid-cols-2 lg:grid-cols-3 gap-4">
        {children}
      </div>
    </div>
  );
}

export default function DocsPage() {
  return (
    <div>
      <div className="mb-8">
        <h1 className="text-2xl font-bold">Documentation</h1>
        <p className="text-gray-500 text-sm mt-1">
          Guides, references, and resources for working with SWAMP.
        </p>
      </div>

      <div className="space-y-10">

        <Section title="Tutorials">
          <DocLinkCard
            title="Getting Started"
            description="Step-by-step walkthrough: create an account, register a Git repository as a package, run your first AI security analysis, and review the resulting findings."
            href="/docs/tutorials/onboarding"
            badge="Start here"
          />
          <DocLinkCard
            title="Analyzing a Private GitHub Repository"
            description="Link your GitHub account, install the SWAMP GitHub App on your organization, and run a security analysis on a repository that requires authentication to clone."
            href="/docs/tutorials/private-repo"
          />
        </Section>

        <Section title="Reference">
          <DocLinkCard
            title="REST API — Swagger UI"
            description="Explore every endpoint interactively. Try requests, inspect schemas, and copy curl examples directly in your browser."
            href="/api/v1/docs"
            external
          />
          <DocLinkCard
            title="OpenAPI Specification"
            description="Download the full OpenAPI 3.0 YAML to generate client SDKs, stub servers, or integrate SWAMP into your CI tooling."
            href="/api/v1/openapi.yaml"
            external
          />
          <DocLinkCard
            title="Acceptable Use Policy"
            description="Review the current SWAMP Acceptable Use Policy, which governs what code and data you may submit for analysis."
            href="/aup-v1.1"
            external
          />
        </Section>

        <Section title="Integrations">
          <DocLinkCard
            title="GitHub Integration"
            description="Link your GitHub account to let SWAMP clone private repositories. Once connected, any accessible GitHub URL can be used as a package."
            href="/settings"
          />
          <DocLinkCard
            title="NRP / ACCESS"
            description="Connect an NRP or ACCESS allocation so analysis jobs run against your HPC quota instead of a personal LLM API key."
            href="/settings"
          />
          <DocLinkCard
            title="API Keys for CI/CD"
            description="Generate a long-lived API key and use SWAMP's REST API to trigger analyses automatically on every pull request."
            href="/api-keys"
          />
          <DocLinkCard
            title="SARIF Export"
            description="Every completed analysis produces a SARIF file. Import it into GitHub code scanning, VS Code's SARIF viewer, or any compatible security dashboard."
            href="/analyses"
          />
        </Section>

        <Section title="Account &amp; Settings">
          <DocLinkCard
            title="Profile &amp; Linked Identities"
            description="View your account details, linked CILogon identities, usage statistics, and version information."
            href="/settings"
          />
          <DocLinkCard
            title="Manage Groups"
            description="Create and manage groups to share projects and findings with teammates. Control who has read-only or write access to each project."
            href="/groups"
          />
        </Section>

      </div>
    </div>
  );
}
