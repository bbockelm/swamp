"use client";

import { useQuery } from "@tanstack/react-query";
import { api, type Analysis, type AggregatedTokenUsage } from "@/lib/api";
import Link from "next/link";
import { AnalysisStatus } from "@/components/AnalysisStatus";

function timeAgo(dateStr: string | null): string {
  if (!dateStr) return "—";
  const seconds = Math.floor(
    (Date.now() - new Date(dateStr).getTime()) / 1000,
  );
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(dateStr).toLocaleDateString();
}

export default function Dashboard() {
  const { data: session, isLoading } = useQuery({
    queryKey: ["session"],
    queryFn: api.auth.me,
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[60vh]">
        <div className="text-gray-400">Loading...</div>
      </div>
    );
  }

  if (!session?.authenticated || !session?.user) {
    return <LandingPage />;
  }

  return <DashboardContent userName={session.user.display_name || session.user.email} />;
}

function LandingPage() {
  return (
    <div className="min-h-screen bg-white">
      {/* Header */}
      <header className="border-b">
        <div className="max-w-5xl mx-auto px-6 py-4 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-xl font-bold text-gray-900">SWAMP</span>
            <span className="text-xs bg-blue-100 text-blue-700 px-2 py-0.5 rounded font-medium">beta</span>
          </div>
          <Link
            href="/login"
            className="text-sm bg-blue-600 text-white px-4 py-2 rounded hover:bg-blue-700 transition"
          >
            Sign In
          </Link>
        </div>
      </header>

      {/* Hero */}
      <section className="max-w-5xl mx-auto px-6 py-16 lg:py-24">
        <div className="max-w-3xl">
          <h1 className="text-4xl lg:text-5xl font-bold text-gray-900 leading-tight">
            AI-Powered Security Analysis for Your Code
          </h1>
          <p className="mt-6 text-lg text-gray-600 leading-relaxed">
            The <strong>Software Assurance Marketplace (SWAMP)</strong> uses AI
            agents to perform deep security analysis of Git repositories. Submit
            your code, and SWAMP automatically identifies vulnerabilities,
            generates SARIF reports, and optionally validates findings with
            proof-of-concept exploits.
          </p>
          <div className="mt-8 flex flex-wrap gap-4">
            <Link
              href="/login"
              className="bg-blue-600 text-white px-6 py-3 rounded-lg hover:bg-blue-700 transition font-medium"
            >
              Get Started
            </Link>
            <a
              href="#how-it-works"
              className="border border-gray-300 text-gray-700 px-6 py-3 rounded-lg hover:bg-gray-50 transition font-medium"
            >
              Learn More
            </a>
          </div>
        </div>
      </section>

      {/* How it works */}
      <section id="how-it-works" className="bg-gray-50 border-y">
        <div className="max-w-5xl mx-auto px-6 py-16">
          <h2 className="text-2xl font-bold text-gray-900 mb-10">How It Works</h2>
          <div className="grid md:grid-cols-3 gap-8">
            <StepCard
              step="1"
              title="Add a Repository"
              description="Create a project and point it at your Git repository. SWAMP supports any public or accessible Git URL — GitHub, GitLab, Bitbucket, and more."
            />
            <StepCard
              step="2"
              title="Run an Analysis"
              description="Trigger a security analysis. An AI agent clones your code, reviews it for vulnerabilities, and produces structured findings in SARIF format."
            />
            <StepCard
              step="3"
              title="Review Findings"
              description="Browse identified vulnerabilities with severity ratings, file locations, and code snippets. Triage findings, track them over time, and export reports."
            />
          </div>
        </div>
      </section>

      {/* Features */}
      <section className="max-w-5xl mx-auto px-6 py-16">
        <h2 className="text-2xl font-bold text-gray-900 mb-10">Features</h2>
        <div className="grid md:grid-cols-2 gap-6">
          <FeatureCard
            title="AI-Driven Analysis"
            description="Uses Claude and other frontier AI models to perform deep semantic security review — going beyond pattern matching to understand code intent and data flow."
          />
          <FeatureCard
            title="SARIF Output"
            description="Results are produced in SARIF (Static Analysis Results Interchange Format), the industry standard for security tooling interoperability."
          />
          <FeatureCard
            title="Exploit Validation"
            description="Optionally validates findings with proof-of-concept exploit generation, helping prioritize real risks over theoretical issues."
          />
          <FeatureCard
            title="Group Collaboration"
            description="Organize projects into groups with role-based access control. Invite team members and share findings across your organization."
          />
          <FeatureCard
            title="Encrypted at Rest"
            description="All analysis results are encrypted with per-analysis keys using AES-256-GCM envelope encryption before storage."
          />
          <FeatureCard
            title="Multi-Package Analysis"
            description="Analyze multiple repositories together in a single run for cross-project vulnerability detection."
          />
        </div>
      </section>

      {/* Documentation */}
      <section id="documentation" className="bg-gray-50 border-y">
        <div className="max-w-5xl mx-auto px-6 py-16">
          <h2 className="text-2xl font-bold text-gray-900 mb-4">Documentation</h2>
          <p className="text-gray-600 mb-10 max-w-2xl">
            Everything you need to get started with SWAMP and integrate it into your workflow.
          </p>
          <div className="grid md:grid-cols-2 lg:grid-cols-3 gap-6">
            <DocCard
              title="Quick Start"
              items={[
                "Sign in with your institutional credentials via CILogon",
                "Create a group to organize your projects",
                "Add a project with a Git repository URL",
                "Click \"Run Analysis\" on the project page",
                "Review findings in the SARIF viewer",
              ]}
            />
            <DocCard
              title="Analysis Pipeline"
              items={[
                "Phase 1: AI agent clones and reviews the codebase for security issues",
                "Phase 2 (optional): Exploit validation generates proof-of-concept tests",
                "Results are encrypted and stored as SARIF, Markdown reports, and logs",
                "Findings are extracted with severity, file location, and code snippets",
              ]}
            />
            <DocCard
              title="Key Concepts"
              items={[
                "Projects — a Git repository to analyze",
                "Packages — versioned snapshots of a project (branch/commit)",
                "Analyses — a security scan run against one or more packages",
                "Findings — individual vulnerabilities discovered by an analysis",
                "Groups — team workspaces with role-based access (admin/member)",
              ]}
            />
            <DocCard
              title="Finding Severities"
              items={[
                "Critical / Error — high-impact vulnerabilities requiring immediate attention",
                "Warning / Medium — moderate issues that should be reviewed",
                "Note / Low / Info — informational or low-risk observations",
                "Findings can be triaged: confirmed, false positive, mitigated, or won't fix",
              ]}
            />
            <DocCard
              title="Access & Authentication"
              items={[
                "Authentication via CILogon (institutional credentials)",
                "Users must agree to the Acceptable Use Policy before access",
                "Projects are scoped to groups — members see only their group's work",
                "Admin, project creator, and user roles control permissions",
                "API keys available for programmatic access",
              ]}
            />
            <DocCard
              title="API & Integrations"
              items={[
                "Full REST API documented in OpenAPI 3.0 format",
                "API keys for CI/CD integration",
                "SARIF export for integration with GitHub, VS Code, and other tools",
                "Markdown reports for human-readable summaries",
              ]}
            />
          </div>
        </div>
      </section>

      {/* Footer */}
      <footer className="border-t">
        <div className="max-w-5xl mx-auto px-6 py-8 flex flex-col sm:flex-row items-center justify-between gap-4">
          <p className="text-sm text-gray-400">
            SWAMP — Software Assurance Marketplace
          </p>
          <Link
            href="/login"
            className="text-sm text-blue-600 hover:underline"
          >
            Sign In
          </Link>
        </div>
      </footer>
    </div>
  );
}

function StepCard({ step, title, description }: { step: string; title: string; description: string }) {
  return (
    <div className="bg-white rounded-lg border p-6">
      <div className="w-8 h-8 rounded-full bg-blue-600 text-white flex items-center justify-center text-sm font-bold mb-4">
        {step}
      </div>
      <h3 className="font-semibold text-gray-900 mb-2">{title}</h3>
      <p className="text-sm text-gray-600 leading-relaxed">{description}</p>
    </div>
  );
}

function FeatureCard({ title, description }: { title: string; description: string }) {
  return (
    <div className="bg-gray-50 rounded-lg border p-5">
      <h3 className="font-semibold text-gray-900 mb-1">{title}</h3>
      <p className="text-sm text-gray-600 leading-relaxed">{description}</p>
    </div>
  );
}

function DocCard({ title, items }: { title: string; items: string[] }) {
  return (
    <div className="bg-white rounded-lg border p-5">
      <h3 className="font-semibold text-gray-900 mb-3">{title}</h3>
      <ul className="space-y-2">
        {items.map((item, i) => (
          <li key={i} className="text-sm text-gray-600 leading-relaxed flex gap-2">
            <span className="text-gray-300 select-none flex-shrink-0">&bull;</span>
            <span>{item}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function DashboardContent({ userName }: { userName: string }) {
  const { data: stats, isLoading } = useQuery({
    queryKey: ["dashboard-stats"],
    queryFn: api.dashboard.stats,
    refetchInterval: 30000,
  });

  const { data: agentStatus } = useQuery({
    queryKey: ["agent-status"],
    queryFn: api.agent.status,
    staleTime: 60000,
  });

  const totalAnalyses = stats
    ? Object.values(stats.analysis_counts).reduce((a, b) => a + b, 0)
    : 0;
  const runningCount = stats?.analysis_counts?.running ?? 0;
  const pendingCount = stats?.analysis_counts?.pending ?? 0;
  const completedCount = stats?.analysis_counts?.completed ?? 0;
  const failedCount = stats?.analysis_counts?.failed ?? 0;
  const activeCount = runningCount + pendingCount;

  const severityOrder = ["critical", "error", "high", "medium", "warning", "low", "note", "info"];
  const sortedSeverities = stats
    ? Object.entries(stats.severity_counts).sort(
        (a, b) =>
          (severityOrder.indexOf(a[0]) === -1 ? 99 : severityOrder.indexOf(a[0])) -
          (severityOrder.indexOf(b[0]) === -1 ? 99 : severityOrder.indexOf(b[0])),
      )
    : [];

  return (
    <div>
      <div className="mb-8">
        <h1 className="text-2xl font-bold">Dashboard</h1>
        <p className="text-gray-500 text-sm mt-1">Welcome back, {userName}</p>
      </div>

      {/* Stats cards */}
      {isLoading ? (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
          {[...Array(4)].map((_, i) => (
            <div key={i} className="h-24 bg-gray-100 rounded-lg animate-pulse" />
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
          <Link href="/projects" className="block">
            <StatCard
              label="Projects"
              value={stats?.project_count ?? 0}
              color="blue"
            />
          </Link>
          <Link href="/analyses" className="block">
            <StatCard
              label="Analyses"
              value={totalAnalyses}
              detail={activeCount > 0 ? `${activeCount} active` : undefined}
              color="indigo"
            />
          </Link>
          <Link href="/groups" className="block">
            <StatCard
              label="Groups"
              value={stats?.group_count ?? 0}
              color="purple"
            />
          </Link>
          <Link href="/findings" className="block">
            <StatCard
              label="Findings"
              value={stats?.total_findings ?? 0}
              detail={
                sortedSeverities.length > 0
                  ? sortedSeverities
                      .slice(0, 3)
                      .map(([k, v]) => `${v} ${k}`)
                      .join(", ")
                  : undefined
              }
              color="amber"
            />
          </Link>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Analysis breakdown */}
        <div className="bg-white rounded-lg border p-5">
          <h2 className="text-sm font-semibold text-gray-500 uppercase tracking-wide mb-4">
            Analysis Overview
          </h2>
          {!stats || totalAnalyses === 0 ? (
            <div className="text-center py-8">
              <p className="text-gray-400 text-sm mb-3">No analyses yet</p>
              <Link
                href="/projects"
                className="text-sm text-blue-600 hover:underline"
              >
                Create a project to get started
              </Link>
            </div>
          ) : (
            <div className="space-y-3">
              <StatusBar
                label="Completed"
                count={completedCount}
                total={totalAnalyses}
                color="bg-green-500"
              />
              <StatusBar
                label="Running"
                count={runningCount}
                total={totalAnalyses}
                color="bg-blue-500"
              />
              <StatusBar
                label="Pending"
                count={pendingCount}
                total={totalAnalyses}
                color="bg-yellow-500"
              />
              <StatusBar
                label="Failed"
                count={failedCount}
                total={totalAnalyses}
                color="bg-red-500"
              />
              {Object.entries(stats.analysis_counts)
                .filter(
                  ([k]) =>
                    !["completed", "running", "pending", "failed"].includes(k),
                )
                .map(([k, v]) => (
                  <StatusBar
                    key={k}
                    label={k.charAt(0).toUpperCase() + k.slice(1)}
                    count={v}
                    total={totalAnalyses}
                    color="bg-gray-400"
                  />
                ))}
            </div>
          )}

          {/* Agent status */}
          {agentStatus && !agentStatus.ready && (
            <div className="mt-4 text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded p-2">
              Analysis agent is not configured.
            </div>
          )}
        </div>

        {/* Severity breakdown */}
        <div className="bg-white rounded-lg border p-5">
          <h2 className="text-sm font-semibold text-gray-500 uppercase tracking-wide mb-4">
            Finding Severity
          </h2>
          {sortedSeverities.length === 0 ? (
            <div className="text-center py-8">
              <p className="text-gray-400 text-sm">No findings recorded yet</p>
            </div>
          ) : (
            <div className="space-y-3">
              {sortedSeverities.map(([sev, count]) => (
                <Link
                  key={sev}
                  href={`/findings?severity=${encodeURIComponent(sev)}`}
                  className="flex items-center justify-between hover:bg-gray-50 -mx-2 px-2 py-1 rounded transition"
                >
                  <div className="flex items-center gap-2">
                    <span
                      className={`w-2.5 h-2.5 rounded-full ${severityColor(sev)}`}
                    />
                    <span className="text-sm capitalize">{sev}</span>
                  </div>
                  <span className="text-sm font-medium tabular-nums">
                    {count}
                  </span>
                </Link>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Token Usage */}
      {stats?.token_usage && stats.token_usage.length > 0 && (
        <div className="mt-6 bg-white rounded-lg border p-5">
          <div className="flex items-baseline gap-2 mb-4">
            <h2 className="text-sm font-semibold text-gray-500 uppercase tracking-wide">
              LLM Usage
            </h2>
            <span className="text-xs text-gray-400">(estimated cost)</span>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-xs text-gray-500 uppercase border-b">
                  <th className="text-left py-1 pr-4">Model</th>
                  <th className="text-right py-1 px-2">Analyses</th>
                  <th className="text-right py-1 px-2">Input</th>
                  <th className="text-right py-1 px-2">Output</th>
                  <th className="text-right py-1 px-2">Cache Read</th>
                  <th className="text-right py-1 px-2">Cache Write</th>
                  <th className="text-right py-1 pl-2">Est. Cost</th>
                </tr>
              </thead>
              <tbody>
                {stats.token_usage.map((u: AggregatedTokenUsage) => (
                  <tr key={u.model} className="border-b border-gray-100">
                    <td className="py-1.5 pr-4 font-mono text-xs">{u.model}</td>
                    <td className="py-1.5 px-2 text-right">{u.analysis_count}</td>
                    <td className="py-1.5 px-2 text-right font-mono">{fmtTok(u.input_tokens)}</td>
                    <td className="py-1.5 px-2 text-right font-mono">{fmtTok(u.output_tokens)}</td>
                    <td className="py-1.5 px-2 text-right font-mono text-gray-400">{fmtTok(u.cache_read_tokens)}</td>
                    <td className="py-1.5 px-2 text-right font-mono text-gray-400">{fmtTok(u.cache_write_tokens)}</td>
                    <td className="py-1.5 pl-2 text-right font-mono">
                      {u.cost_usd > 0 ? `$${u.cost_usd.toFixed(2)}` : "—"}
                    </td>
                  </tr>
                ))}
                {stats.token_usage.length > 1 && (
                  <tr className="font-semibold">
                    <td className="py-1.5 pr-4 text-xs">Total</td>
                    <td className="py-1.5 px-2 text-right">
                      {stats.token_usage.reduce((s: number, u: AggregatedTokenUsage) => s + u.analysis_count, 0)}
                    </td>
                    <td className="py-1.5 px-2 text-right font-mono">
                      {fmtTok(stats.token_usage.reduce((s: number, u: AggregatedTokenUsage) => s + u.input_tokens, 0))}
                    </td>
                    <td className="py-1.5 px-2 text-right font-mono">
                      {fmtTok(stats.token_usage.reduce((s: number, u: AggregatedTokenUsage) => s + u.output_tokens, 0))}
                    </td>
                    <td className="py-1.5 px-2 text-right font-mono text-gray-400">
                      {fmtTok(stats.token_usage.reduce((s: number, u: AggregatedTokenUsage) => s + u.cache_read_tokens, 0))}
                    </td>
                    <td className="py-1.5 px-2 text-right font-mono text-gray-400">
                      {fmtTok(stats.token_usage.reduce((s: number, u: AggregatedTokenUsage) => s + u.cache_write_tokens, 0))}
                    </td>
                    <td className="py-1.5 pl-2 text-right font-mono">
                      ${stats.token_usage.reduce((s: number, u: AggregatedTokenUsage) => s + u.cost_usd, 0).toFixed(2)}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Recent analyses */}
      <div className="mt-6 bg-white rounded-lg border p-5">
        <div className="flex justify-between items-center mb-4">
          <h2 className="text-sm font-semibold text-gray-500 uppercase tracking-wide">
            Recent Analyses
          </h2>
          <Link
            href="/analyses"
            className="text-xs text-blue-600 hover:underline"
          >
            View all
          </Link>
        </div>
        {!stats?.recent_analyses?.length ? (
          <p className="text-gray-400 text-sm text-center py-4">
            No recent analyses.
          </p>
        ) : (
          <div className="divide-y">
            {stats.recent_analyses.map((a: Analysis) => (
              <Link
                key={a.id}
                href={`/projects/${a.project_id}/analyses/${a.id}`}
                className="flex items-center justify-between py-3 hover:bg-gray-50 -mx-2 px-2 rounded transition"
              >
                <div className="flex items-center gap-3 min-w-0">
                  <AnalysisStatus status={a.status} />
                  <div className="min-w-0">
                    <span className="text-sm font-medium truncate block">
                      {a.project_name || a.project_id.slice(0, 8)}
                    </span>
                    <span className="text-xs text-gray-400 font-mono">
                      {a.id.slice(0, 8)}
                    </span>
                  </div>
                </div>
                <div className="text-right flex-shrink-0 ml-4">
                  <span className="text-xs text-gray-400">
                    {timeAgo(a.created_at)}
                  </span>
                  {a.error_message && (
                    <p className="text-xs text-red-500 truncate max-w-[200px]">
                      {a.error_message}
                    </p>
                  )}
                </div>
              </Link>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function StatCard({
  label,
  value,
  detail,
  color,
}: {
  label: string;
  value: number;
  detail?: string;
  color: string;
}) {
  const borderColors: Record<string, string> = {
    blue: "border-l-blue-500",
    indigo: "border-l-indigo-500",
    purple: "border-l-purple-500",
    amber: "border-l-amber-500",
    green: "border-l-green-500",
    red: "border-l-red-500",
  };

  return (
    <div
      className={`bg-white rounded-lg border border-l-4 ${borderColors[color] || "border-l-gray-500"} p-4 hover:shadow-md transition`}
    >
      <p className="text-xs text-gray-500 uppercase tracking-wide">{label}</p>
      <p className="text-2xl font-bold mt-1">{value.toLocaleString()}</p>
      {detail && <p className="text-xs text-gray-400 mt-1">{detail}</p>}
    </div>
  );
}

function StatusBar({
  label,
  count,
  total,
  color,
}: {
  label: string;
  count: number;
  total: number;
  color: string;
}) {
  const pct = total > 0 ? (count / total) * 100 : 0;
  return (
    <div>
      <div className="flex justify-between text-sm mb-1">
        <span className="text-gray-600">{label}</span>
        <span className="tabular-nums font-medium">
          {count}
          <span className="text-gray-400 font-normal ml-1 text-xs">
            ({pct.toFixed(0)}%)
          </span>
        </span>
      </div>
      <div className="h-2 bg-gray-100 rounded-full overflow-hidden">
        <div
          className={`h-full ${color} rounded-full transition-all`}
          style={{ width: `${Math.max(pct, count > 0 ? 2 : 0)}%` }}
        />
      </div>
    </div>
  );
}

function fmtTok(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return n.toString();
}

function severityColor(sev: string): string {
  switch (sev.toLowerCase()) {
    case "critical":
      return "bg-red-700";
    case "error":
    case "high":
      return "bg-red-500";
    case "medium":
    case "warning":
      return "bg-amber-500";
    case "low":
      return "bg-yellow-400";
    case "note":
    case "info":
      return "bg-blue-400";
    default:
      return "bg-gray-400";
  }
}
