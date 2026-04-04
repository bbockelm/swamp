"use client";

import { useQuery } from "@tanstack/react-query";
import { api, type Analysis } from "@/lib/api";
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
  const { data: session } = useQuery({
    queryKey: ["session"],
    queryFn: api.auth.me,
  });

  if (!session?.user) {
    return (
      <div className="flex items-center justify-center min-h-[60vh]">
        <div className="text-center">
          <h1 className="text-3xl font-bold mb-4">SWAMP</h1>
          <p className="text-gray-600 mb-8">
            Software Assurance Marketplace — AI-powered security analysis
          </p>
          <a
            href="/api/v1/auth/oidc/login"
            className="bg-blue-600 text-white px-6 py-3 rounded-lg hover:bg-blue-700"
          >
            Sign in with CILogon
          </a>
        </div>
      </div>
    );
  }

  return <DashboardContent userName={session.user.display_name || session.user.email} />;
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
