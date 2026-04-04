"use client";

import { useState, useEffect, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import {
  api,
  type Project,
  type Group,
  type Session,
  type SoftwarePackage,
  type Analysis,
  type AnalysisResult,
  type User,
} from "@/lib/api";
import { AnalysisStatus } from "@/components/AnalysisStatus";
import { Pagination, paginate } from "@/components/Pagination";
import { SARIFViewer } from "@/components/SARIFViewer";
import { MarkdownReport } from "@/components/MarkdownReport";
import { GitBranchInput } from "@/components/GitBranchInput";

const ANALYSES_PAGE_SIZE = 10;

// ─── helpers ────────────────────────────────────────────────

function GroupSearch({
  label,
  value,
  groups,
  onChange,
  disabled,
}: {
  label: string;
  value: string;
  groups?: Group[];
  onChange: (v: string) => void;
  disabled?: boolean;
}) {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  const selectedGroup = groups?.find((g) => g.id === value);

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  const filtered = (groups ?? []).filter(
    (g) =>
      g.name.toLowerCase().includes(query.toLowerCase()) ||
      g.description?.toLowerCase().includes(query.toLowerCase()),
  );

  return (
    <div ref={ref} className="relative">
      <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
        {label}
      </label>
      <div className="relative">
        <input
          type="text"
          value={open ? query : selectedGroup?.name ?? ""}
          onChange={(e) => {
            setQuery(e.target.value);
            if (!open) setOpen(true);
          }}
          onFocus={() => {
            setOpen(true);
            setQuery("");
          }}
          disabled={disabled}
          className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:bg-gray-100 disabled:text-gray-400"
          placeholder="Search groups..."
        />
        {value && !disabled && (
          <button
            type="button"
            onClick={() => {
              onChange("");
              setQuery("");
            }}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        )}
      </div>
      {open && !disabled && (
        <div className="absolute z-10 mt-1 w-full bg-white border rounded shadow-lg max-h-48 overflow-auto">
          {filtered.length === 0 ? (
            <div className="px-3 py-2 text-sm text-gray-400">No groups found</div>
          ) : (
            filtered.map((g) => (
              <button
                key={g.id}
                type="button"
                onClick={() => {
                  onChange(g.id);
                  setOpen(false);
                  setQuery("");
                }}
                className={`w-full text-left px-3 py-2 text-sm hover:bg-blue-50 ${
                  g.id === value ? "bg-blue-50 font-medium" : ""
                }`}
              >
                {g.name}
                {g.description && (
                  <span className="text-gray-400 ml-1 text-xs">{g.description}</span>
                )}
              </button>
            ))
          )}
        </div>
      )}
    </div>
  );
}

// ─── main page ──────────────────────────────────────────────

export default function ProjectsPage() {
  const queryClient = useQueryClient();

  const { data: session } = useQuery<Session>({
    queryKey: ["session"],
    queryFn: api.auth.me,
  });

  const { data: projects, isLoading } = useQuery<Project[]>({
    queryKey: ["projects"],
    queryFn: api.projects.list,
  });

  const { data: groups } = useQuery<Group[]>({
    queryKey: ["groups"],
    queryFn: api.groups.list,
  });

  const { data: users } = useQuery<User[]>({
    queryKey: ["admin", "users"],
    queryFn: api.admin.listUsers,
    enabled: session?.roles?.includes("admin") ?? false,
  });

  const isAdmin = session?.roles?.includes("admin") ?? false;

  const [showCreateForm, setShowCreateForm] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [autoCreateGroups, setAutoCreateGroups] = useState(true);
  const [readGroupId, setReadGroupId] = useState("");
  const [writeGroupId, setWriteGroupId] = useState("");
  const [adminGroupId, setAdminGroupId] = useState("");
  const [ownerId, setOwnerId] = useState("");

  const [expandedId, setExpandedId] = useState<string | null>(null);

  const createProject = useMutation({
    mutationFn: async () => {
      let rg = readGroupId || null;
      let wg = writeGroupId || null;
      let ag = adminGroupId || null;

      if (autoCreateGroups && name.trim()) {
        const [readGroup, writeGroup, adminGroup] = await Promise.all([
          api.groups.create({ name: `${name.trim()} - Read`, description: `Read access for ${name.trim()}` }),
          api.groups.create({ name: `${name.trim()} - Write`, description: `Write access for ${name.trim()}` }),
          api.groups.create({ name: `${name.trim()} - Admin`, description: `Admin access for ${name.trim()}` }),
        ]);
        rg = readGroup.id;
        wg = writeGroup.id;
        ag = adminGroup.id;
      }

      return api.projects.create({
        name,
        description,
        read_group_id: rg,
        write_group_id: wg,
        admin_group_id: ag,
        ...(isAdmin && ownerId ? { owner_id: ownerId } : {}),
      });
    },
    onSuccess: (project) => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
      queryClient.invalidateQueries({ queryKey: ["groups"] });
      setName("");
      setDescription("");
      setAutoCreateGroups(true);
      setReadGroupId("");
      setWriteGroupId("");
      setAdminGroupId("");
      setOwnerId("");
      setShowCreateForm(false);
      setExpandedId(project.id);
    },
  });

  if (isLoading) return <p>Loading...</p>;

  return (
    <div>
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-2xl font-bold">Projects</h1>
        <button
          onClick={() => setShowCreateForm(!showCreateForm)}
          className={`px-4 py-2 rounded text-sm font-medium transition-colors ${
            showCreateForm
              ? "bg-gray-200 text-gray-700 hover:bg-gray-300"
              : "bg-blue-600 text-white hover:bg-blue-700"
          }`}
        >
          {showCreateForm ? "Cancel" : "+ New Project"}
        </button>
      </div>

      {/* Inline create form */}
      {showCreateForm && (
        <div className="mb-6 p-4 bg-gray-50 border rounded-lg">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              createProject.mutate();
            }}
            className="space-y-3"
          >
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
                  Project Name
                </label>
                <input
                  type="text"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="My Security Project"
                  autoFocus
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
                  Description
                </label>
                <input
                  type="text"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="Optional description..."
                />
              </div>
            </div>

            <div>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={autoCreateGroups}
                  onChange={(e) => {
                    setAutoCreateGroups(e.target.checked);
                    if (e.target.checked) {
                      setReadGroupId("");
                      setWriteGroupId("");
                      setAdminGroupId("");
                    }
                  }}
                />
                <span className="font-medium text-gray-700">
                  Auto-create groups
                </span>
              </label>
              {autoCreateGroups && name.trim() && (
                <p className="text-xs text-gray-400 mt-1 ml-6">
                  Will create: <span className="font-medium">{name.trim()} - Read</span>,{" "}
                  <span className="font-medium">{name.trim()} - Write</span>,{" "}
                  <span className="font-medium">{name.trim()} - Admin</span>
                </p>
              )}
            </div>

            {!autoCreateGroups && (
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <GroupSearch
                  label="Read Access Group"
                  value={readGroupId}
                  groups={groups}
                  onChange={setReadGroupId}
                />
                <GroupSearch
                  label="Write Access Group"
                  value={writeGroupId}
                  groups={groups}
                  onChange={setWriteGroupId}
                />
                <GroupSearch
                  label="Admin Group"
                  value={adminGroupId}
                  groups={groups}
                  onChange={setAdminGroupId}
                />
              </div>
            )}

            {isAdmin && (
              <div className="max-w-xs">
                <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
                  Owner
                </label>
                <select
                  value={ownerId}
                  onChange={(e) => setOwnerId(e.target.value)}
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                >
                  <option value="">Myself</option>
                  {users?.map((u) => (
                    <option key={u.id} value={u.id}>
                      {u.display_name || u.email || u.id}
                    </option>
                  ))}
                </select>
              </div>
            )}

            <div className="flex gap-2">
              <button
                type="submit"
                disabled={createProject.isPending || !name.trim()}
                className="px-4 py-2 bg-green-600 text-white rounded text-sm font-medium hover:bg-green-700 disabled:opacity-50"
              >
                {createProject.isPending ? "Creating..." : "Create"}
              </button>
            </div>
            {createProject.isError && (
              <p className="text-sm text-red-600">
                Error: {createProject.error?.message || 'An unexpected error occurred'}
              </p>
            )}
          </form>
        </div>
      )}

      {!projects?.length ? (
        <p className="text-gray-500">No projects yet.</p>
      ) : (
        <div className="space-y-2">
          {projects.map((p) => (
            <ProjectCard
              key={p.id}
              project={p}
              groups={groups}
              users={users}
              expanded={expandedId === p.id}
              onToggle={() => setExpandedId(expandedId === p.id ? null : p.id)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ─── project card ───────────────────────────────────────────

type ProjectTab = "packages" | "analyses" | "settings";

function ProjectCard({
  project,
  groups,
  users,
  expanded,
  onToggle,
}: {
  project: Project;
  groups?: Group[];
  users?: User[];
  expanded: boolean;
  onToggle: () => void;
}) {
  const [tab, setTab] = useState<ProjectTab>("packages");

  const groupName = (id: string | null) => {
    if (!id) return null;
    return groups?.find((g) => g.id === id)?.name ?? id.slice(0, 8);
  };

  const ownerName = () => {
    if (!users) return null;
    const u = users.find((u) => u.id === project.owner_id);
    return u?.display_name || u?.email || null;
  };

  const tabs: { key: ProjectTab; label: string }[] = [
    { key: "packages", label: "Packages" },
    { key: "analyses", label: "Analyses" },
    { key: "settings", label: "Settings" },
  ];

  return (
    <div className="border rounded-lg bg-white shadow-sm">
      {/* Summary row */}
      <button
        onClick={onToggle}
        className="w-full flex items-center gap-4 px-4 py-3 text-left hover:bg-gray-50 transition-colors"
      >
        <div className="flex-1 min-w-0">
          <div className="font-medium text-gray-900">{project.name}</div>
          {project.description && (
            <div className="text-sm text-gray-500 truncate">
              {project.description}
            </div>
          )}
        </div>

        {/* Access badges */}
        <div className="hidden sm:flex items-center gap-2 flex-shrink-0">
          {groupName(project.read_group_id) && (
            <span className="text-xs px-2 py-0.5 rounded-full bg-green-100 text-green-800">
              R: {groupName(project.read_group_id)}
            </span>
          )}
          {groupName(project.write_group_id) && (
            <span className="text-xs px-2 py-0.5 rounded-full bg-blue-100 text-blue-800">
              W: {groupName(project.write_group_id)}
            </span>
          )}
          {groupName(project.admin_group_id) && (
            <span className="text-xs px-2 py-0.5 rounded-full bg-purple-100 text-purple-800">
              A: {groupName(project.admin_group_id)}
            </span>
          )}
        </div>

        {ownerName() && (
          <span className="hidden md:inline text-xs text-gray-400">
            {ownerName()}
          </span>
        )}

        <span className="text-xs text-gray-400 flex-shrink-0">
          {new Date(project.created_at).toLocaleDateString()}
        </span>

        <Link
          href={`/projects/${project.id}`}
          onClick={(e) => e.stopPropagation()}
          className="p-1 text-gray-400 hover:text-blue-600 flex-shrink-0"
          title="Open project page"
        >
          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
          </svg>
        </Link>

        <svg
          className={`w-5 h-5 text-gray-400 transition-transform flex-shrink-0 ${
            expanded ? "rotate-180" : ""
          }`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={2}
            d="M19 9l-7 7-7-7"
          />
        </svg>
      </button>

      {/* Expanded detail */}
      {expanded && (
        <div className="border-t bg-gray-50">
          {/* Tabs */}
          <div className="px-4 pt-3 border-b bg-white">
            <div className="flex gap-4">
              {tabs.map((t) => (
                <button
                  key={t.key}
                  onClick={() => setTab(t.key)}
                  className={`pb-2 px-1 text-sm font-medium border-b-2 ${
                    tab === t.key
                      ? "border-blue-600 text-blue-600"
                      : "border-transparent text-gray-500 hover:text-gray-700"
                  }`}
                >
                  {t.label}
                </button>
              ))}
            </div>
          </div>

          <div className="p-4">
            {tab === "packages" && <PackagesTab projectId={project.id} />}
            {tab === "analyses" && <AnalysesTab projectId={project.id} />}
            {tab === "settings" && (
              <SettingsTab
                project={project}
                groups={groups}
              />
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// ─── packages tab ───────────────────────────────────────────

function PackagesTab({ projectId }: { projectId: string }) {
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [gitUrl, setGitUrl] = useState("");
  const [gitBranch, setGitBranch] = useState("main");
  const [analysisPrompt, setAnalysisPrompt] = useState("");

  const { data: packages } = useQuery({
    queryKey: ["packages", projectId],
    queryFn: () => api.packages.list(projectId),
  });

  const createMutation = useMutation({
    mutationFn: () =>
      api.packages.create(projectId, {
        name,
        git_url: gitUrl,
        git_branch: gitBranch,
        analysis_prompt: analysisPrompt,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["packages", projectId] });
      setAdding(false);
      setName("");
      setGitUrl("");
      setGitBranch("main");
      setAnalysisPrompt("");
    },
  });

  const updateMutation = useMutation({
    mutationFn: (pkgId: string) =>
      api.packages.update(projectId, pkgId, {
        name,
        git_url: gitUrl,
        git_branch: gitBranch,
        analysis_prompt: analysisPrompt,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["packages", projectId] });
      setEditingId(null);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (pkgId: string) => api.packages.delete(projectId, pkgId),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ["packages", projectId] }),
  });

  const startEdit = (pkg: SoftwarePackage) => {
    setEditingId(pkg.id);
    setName(pkg.name);
    setGitUrl(pkg.git_url);
    setGitBranch(pkg.git_branch);
    setAnalysisPrompt(pkg.analysis_prompt || "");
    setAdding(false);
  };

  const cancelEdit = () => {
    setEditingId(null);
    setName("");
    setGitUrl("");
    setGitBranch("main");
    setAnalysisPrompt("");
  };

  return (
    <div>
      <div className="flex justify-between items-center mb-4">
        <h3 className="text-sm font-semibold uppercase text-gray-500 tracking-wide">
          Software Packages
        </h3>
        <button
          onClick={() => {
            setAdding(!adding);
            setEditingId(null);
            if (!adding) {
              setName("");
              setGitUrl("");
              setGitBranch("main");
              setAnalysisPrompt("");
            }
          }}
          className={`px-3 py-1.5 text-sm rounded font-medium transition-colors ${
            adding
              ? "bg-gray-200 text-gray-700 hover:bg-gray-300"
              : "bg-blue-600 text-white hover:bg-blue-700"
          }`}
        >
          {adding ? "Cancel" : "+ Add Package"}
        </button>
      </div>

      {adding && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            createMutation.mutate();
          }}
          className="bg-white p-4 rounded border mb-4 space-y-3"
        >
          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">
              Git URL
            </label>
            <input
              type="url"
              value={gitUrl}
              onChange={(e) => setGitUrl(e.target.value)}
              required
              className="w-full border rounded px-3 py-2 text-sm"
              placeholder="https://github.com/org/repo.git"
              autoFocus
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium text-gray-500 mb-1">
                Name
              </label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
                className="w-full border rounded px-3 py-2 text-sm"
              />
            </div>
            <GitBranchInput
              gitUrl={gitUrl}
              value={gitBranch}
              onChange={setGitBranch}
              labelClassName="block text-xs font-medium text-gray-500 mb-1"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-500 mb-1">
              Custom Analysis Prompt
            </label>
            <textarea
              value={analysisPrompt}
              onChange={(e) => setAnalysisPrompt(e.target.value)}
              rows={2}
              className="w-full border rounded px-3 py-2 text-sm"
              placeholder="Focus on authentication and SQL injection..."
            />
          </div>
          <button
            type="submit"
            disabled={createMutation.isPending}
            className="bg-green-600 text-white px-3 py-1.5 text-sm rounded hover:bg-green-700 disabled:opacity-50"
          >
            {createMutation.isPending ? "Adding..." : "Add"}
          </button>
          {createMutation.isError && (
            <p className="text-sm text-red-600">
              {createMutation.error?.message || 'An unexpected error occurred'}
            </p>
          )}
        </form>
      )}

      {!packages?.length ? (
        <p className="text-gray-500 text-sm">
          No packages yet. Add a Git repository to analyze.
        </p>
      ) : (
        <div className="space-y-2">
          {packages.map((pkg) =>
            editingId === pkg.id ? (
              <form
                key={pkg.id}
                onSubmit={(e) => {
                  e.preventDefault();
                  updateMutation.mutate(pkg.id);
                }}
                className="bg-white p-4 rounded border space-y-3"
              >
                <div>
                  <label className="block text-xs font-medium text-gray-500 mb-1">
                    Git URL
                  </label>
                  <input
                    type="url"
                    value={gitUrl}
                    onChange={(e) => setGitUrl(e.target.value)}
                    required
                    className="w-full border rounded px-3 py-2 text-sm"
                  />
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-xs font-medium text-gray-500 mb-1">
                      Name
                    </label>
                    <input
                      type="text"
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      required
                      className="w-full border rounded px-3 py-2 text-sm"
                    />
                  </div>
                  <GitBranchInput
                    gitUrl={gitUrl}
                    value={gitBranch}
                    onChange={setGitBranch}
                    labelClassName="block text-xs font-medium text-gray-500 mb-1"
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium text-gray-500 mb-1">
                    Custom Analysis Prompt
                  </label>
                  <textarea
                    value={analysisPrompt}
                    onChange={(e) => setAnalysisPrompt(e.target.value)}
                    rows={2}
                    className="w-full border rounded px-3 py-2 text-sm"
                  />
                </div>
                <div className="flex gap-2">
                  <button
                    type="submit"
                    disabled={updateMutation.isPending}
                    className="bg-blue-600 text-white px-3 py-1.5 text-sm rounded hover:bg-blue-700 disabled:opacity-50"
                  >
                    {updateMutation.isPending ? "Saving..." : "Save"}
                  </button>
                  <button
                    type="button"
                    onClick={cancelEdit}
                    className="px-3 py-1.5 text-sm border rounded hover:bg-gray-100"
                  >
                    Cancel
                  </button>
                </div>
              </form>
            ) : (
              <div
                key={pkg.id}
                className="p-3 bg-white border rounded flex justify-between items-start"
              >
                <div>
                  <span className="font-medium text-sm">{pkg.name}</span>
                  <span className="text-xs text-gray-400 font-mono ml-2">
                    {pkg.git_url}
                  </span>
                  <div className="text-xs text-gray-400 mt-0.5">
                    Branch: {pkg.git_branch}
                    {pkg.git_commit && ` · ${pkg.git_commit.slice(0, 8)}`}
                  </div>
                  {pkg.analysis_prompt && (
                    <div className="text-xs text-gray-400 mt-0.5 italic">
                      Prompt: {pkg.analysis_prompt.length > 80 ? pkg.analysis_prompt.slice(0, 80) + "…" : pkg.analysis_prompt}
                    </div>
                  )}
                </div>
                <div className="flex gap-2">
                  <button
                    onClick={() => startEdit(pkg)}
                    className="text-blue-500 text-xs hover:text-blue-700"
                  >
                    Edit
                  </button>
                  <button
                    onClick={() => {
                      if (confirm("Delete this package?"))
                        deleteMutation.mutate(pkg.id);
                    }}
                    className="text-red-500 text-xs hover:text-red-700"
                  >
                    Delete
                  </button>
                </div>
              </div>
            )
          )}
        </div>
      )}
    </div>
  );
}

// ─── analyses tab ───────────────────────────────────────────

function AnalysesTab({ projectId }: { projectId: string }) {
  const queryClient = useQueryClient();
  const [selectedPkgs, setSelectedPkgs] = useState<string[]>([]);
  const [customPrompt, setCustomPrompt] = useState("");
  const [openAnalysis, setOpenAnalysis] = useState<string | null>(null);
  const [analysisPage, setAnalysisPage] = useState(1);

  const { data: agentStatus } = useQuery({
    queryKey: ['agent-status'],
    queryFn: () => api.agent.status(),
    staleTime: 60_000,
  });

  const { data: packages } = useQuery({
    queryKey: ["packages", projectId],
    queryFn: () => api.packages.list(projectId),
  });

  const { data: analyses } = useQuery({
    queryKey: ["analyses", projectId],
    queryFn: () => api.analyses.list(projectId),
    refetchInterval: (query) => {
      const list = query.state.data;
      if (
        list?.some(
          (a: Analysis) => a.status === "pending" || a.status === "running",
        )
      ) {
        return 5000;
      }
      return false;
    },
  });

  const triggerMutation = useMutation({
    mutationFn: () =>
      api.analyses.create(projectId, {
        package_ids: selectedPkgs,
        custom_prompt: customPrompt || undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["analyses", projectId] });
      setSelectedPkgs([]);
      setCustomPrompt("");
    },
  });

  return (
    <div>
      {/* Trigger new analysis */}
      {packages && packages.length > 0 && (
        <div className="bg-white p-4 rounded border mb-4">
          <h3 className="text-sm font-medium mb-2">Run New Analysis</h3>
          {agentStatus && !agentStatus.ready && (
            <div className="text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded p-2 mb-3">
              Analysis agent is not configured. Set <code className="bg-amber-100 px-1 rounded">AGENT_API_KEY</code> or <code className="bg-amber-100 px-1 rounded">AGENT_API_KEY_FILE</code> to enable.
            </div>
          )}
          <div className="space-y-1 mb-3">
            {packages.map((pkg) => (
              <label key={pkg.id} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={selectedPkgs.includes(pkg.id)}
                  onChange={(e) =>
                    setSelectedPkgs((prev) =>
                      e.target.checked
                        ? [...prev, pkg.id]
                        : prev.filter((x) => x !== pkg.id),
                    )
                  }
                />
                {pkg.name} ({pkg.git_branch})
              </label>
            ))}
          </div>
          <div className="mb-3">
            <label className="block text-xs font-medium text-gray-500 mb-1">
              Additional Prompt
            </label>
            <textarea
              value={customPrompt}
              onChange={(e) => setCustomPrompt(e.target.value)}
              rows={2}
              className="w-full border rounded px-3 py-2 text-sm"
              placeholder="Focus on specific areas, e.g. 'Pay special attention to the OAuth flow and token handling...'"
            />
          </div>
          <button
            onClick={() => triggerMutation.mutate()}
            disabled={!selectedPkgs.length || triggerMutation.isPending || !agentStatus?.ready}
            className="bg-green-600 text-white px-3 py-1.5 text-sm rounded hover:bg-green-700 disabled:opacity-50"
          >
            {triggerMutation.isPending ? "Starting..." : "Start Analysis"}
          </button>
        </div>
      )}

      <h3 className="text-sm font-semibold uppercase text-gray-500 tracking-wide mb-3">
        Analysis History
      </h3>
      {!analyses?.length ? (
        <p className="text-gray-500 text-sm">No analyses yet.</p>
      ) : (
        <>
          <div className="space-y-2">
            {paginate(analyses, analysisPage, ANALYSES_PAGE_SIZE).map((a) => (
              <AnalysisCard
                key={a.id}
                analysis={a}
                projectId={projectId}
                expanded={openAnalysis === a.id}
                onToggle={() =>
                  setOpenAnalysis(openAnalysis === a.id ? null : a.id)
                }
              />
            ))}
          </div>
          <Pagination
            currentPage={analysisPage}
            totalPages={Math.ceil(analyses.length / ANALYSES_PAGE_SIZE)}
            onPageChange={setAnalysisPage}
          />
        </>
      )}
    </div>
  );
}

// ─── duration helpers ───────────────────────────────────────

function humanDelta(from: string, to: string): string {
  const ms = new Date(to).getTime() - new Date(from).getTime();
  if (ms < 0) return "0s";
  const secs = Math.floor(ms / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const remSecs = secs % 60;
  if (mins < 60) return remSecs > 0 ? `${mins}m ${remSecs}s` : `${mins}m`;
  const hrs = Math.floor(mins / 60);
  const remMins = mins % 60;
  return remMins > 0 ? `${hrs}h ${remMins}m` : `${hrs}h`;
}

function formatDuration(a: Analysis): string | null {
  if (a.completed_at) return humanDelta(a.created_at, a.completed_at);
  // For active jobs, formatDuration is only used as static fallback;
  // the live-ticking ElapsedTime component is used instead.
  if (a.started_at)
    return humanDelta(a.created_at, new Date().toISOString()) + " so far";
  if (a.status === "pending") return "pending";
  return null;
}

/** Ticking elapsed time for active analyses. */
function ElapsedTime({ since }: { since: string }) {
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(id);
  }, []);
  return <>{humanDelta(since, new Date().toISOString())}</>;
}

// ─── analysis card (expandable) ─────────────────────────────

function AnalysisCard({
  analysis,
  projectId,
  expanded,
  onToggle,
}: {
  analysis: Analysis;
  projectId: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  const queryClient = useQueryClient();

  const { data: results } = useQuery({
    queryKey: ["results", projectId, analysis.id],
    queryFn: () => api.analyses.listResults(projectId, analysis.id),
    enabled:
      expanded &&
      (analysis.status === "completed" || analysis.status === "failed"),
  });

  const cancelMutation = useMutation({
    mutationFn: () => api.analyses.cancel(projectId, analysis.id),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["analyses", projectId],
      }),
  });

  const sarifResult = results?.find((r) => r.result_type === "sarif");
  const markdownResult = results?.find(
    (r) => r.result_type === "markdown" || r.result_type === "markdown_report",
  );
  const logResults =
    results?.filter((r) => r.result_type === "agent_log") ?? [];

  return (
    <div className="border rounded bg-white">
      <button
        onClick={onToggle}
        className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-gray-50"
      >
        <div className="flex items-center gap-3">
          <span className="font-mono text-sm text-gray-600">
            {analysis.id.slice(0, 8)}
          </span>
          <AnalysisStatus status={analysis.status} />
          {analysis.status_detail && (
            <span className="text-xs text-gray-400 hidden sm:inline">
              {analysis.status_detail}
            </span>
          )}
        </div>
        <div className="flex items-center gap-3">
          {/* Show most relevant time + duration */}
          <span className="text-xs text-gray-400">
            {analysis.completed_at
              ? new Date(analysis.completed_at).toLocaleString()
              : analysis.started_at
                ? new Date(analysis.started_at).toLocaleString()
                : new Date(analysis.created_at).toLocaleString()}
          </span>
          {(analysis.status === 'running' || analysis.status === 'pending') ? (
            <span className="text-xs text-gray-400 inline-flex items-center gap-1">
              <svg className="w-3 h-3 animate-spin text-blue-500" viewBox="0 0 24 24" fill="none">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
              </svg>
              <ElapsedTime since={analysis.started_at || analysis.created_at} />
            </span>
          ) : formatDuration(analysis) ? (
            <span className="text-xs text-gray-400">
              ({formatDuration(analysis)})
            </span>
          ) : null}
          <Link
            href={`/projects/${projectId}/analyses/${analysis.id}`}
            onClick={(e) => e.stopPropagation()}
            className="p-1 text-gray-400 hover:text-blue-600"
            title="Open analysis page"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
            </svg>
          </Link>
          <svg
            className={`w-4 h-4 text-gray-400 transition-transform ${
              expanded ? "rotate-180" : ""
            }`}
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M19 9l-7 7-7-7"
            />
          </svg>
        </div>
      </button>

      {expanded && (
        <div className="border-t px-4 py-4 space-y-4 bg-gray-50">
          {/* Metadata */}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-sm">
            <div>
              <span className="text-xs text-gray-500 uppercase">Created</span>
              <p>{new Date(analysis.created_at).toLocaleString()}</p>
              <p className="text-xs text-gray-400">{humanDelta(analysis.created_at, new Date().toISOString())} ago</p>
            </div>
            {analysis.started_at && (
              <div>
                <span className="text-xs text-gray-500 uppercase">Started</span>
                <p>{new Date(analysis.started_at).toLocaleString()}</p>
                <p className="text-xs text-gray-400">
                  {humanDelta(analysis.created_at, analysis.started_at)} wait
                </p>
              </div>
            )}
            {analysis.completed_at && (
              <div>
                <span className="text-xs text-gray-500 uppercase">
                  {analysis.status === "cancelled" ? "Cancelled" : "Completed"}
                </span>
                <p>{new Date(analysis.completed_at).toLocaleString()}</p>
                {analysis.started_at && (
                  <p className="text-xs text-gray-400">
                    {humanDelta(analysis.started_at, analysis.completed_at)} run
                  </p>
                )}
              </div>
            )}
            {formatDuration(analysis) && (
              <div>
                <span className="text-xs text-gray-500 uppercase">
                  Total Duration
                </span>
                <p>{formatDuration(analysis)}</p>
              </div>
            )}
            {analysis.triggered_by && (
              <div>
                <span className="text-xs text-gray-500 uppercase">Triggered By</span>
                <p>{analysis.triggered_by_name || analysis.triggered_by.slice(0, 8)}</p>
              </div>
            )}
            {analysis.git_commit && (
              <div>
                <span className="text-xs text-gray-500 uppercase">Commit</span>
                <p className="font-mono">{analysis.git_commit.slice(0, 12)}</p>
              </div>
            )}
          </div>

          {analysis.error_message && (
            <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded p-3">
              {analysis.error_message}
            </div>
          )}

          {/* Cancel + Live output */}
          {(analysis.status === "pending" || analysis.status === "running") && (
            <div className="space-y-3">
              <button
                onClick={() => cancelMutation.mutate()}
                disabled={cancelMutation.isPending}
                className="bg-red-600 text-white px-3 py-1 text-sm rounded hover:bg-red-700 disabled:opacity-50"
              >
                Cancel Analysis
              </button>
              <TerminalStream analysisId={analysis.id} />
            </div>
          )}

          {/* Results */}
          {results && results.length > 0 && (
            <div className="space-y-4">
              {markdownResult && (
                <div>
                  <div className="flex justify-between items-center mb-2">
                    <h4 className="font-medium text-sm">Security Report</h4>
                    <a
                      href={api.analyses.downloadResult(
                        projectId,
                        analysis.id,
                        markdownResult.id,
                      )}
                      className="text-blue-600 text-xs hover:underline"
                    >
                      Download
                    </a>
                  </div>
                  <MarkdownReport
                    projectId={projectId}
                    analysisId={analysis.id}
                    resultId={markdownResult.id}
                  />
                </div>
              )}

              {sarifResult && (
                <div>
                  <div className="flex justify-between items-center mb-2">
                    <h4 className="font-medium text-sm">
                      Findings ({sarifResult.finding_count})
                    </h4>
                    <a
                      href={api.analyses.downloadResult(
                        projectId,
                        analysis.id,
                        sarifResult.id,
                      )}
                      className="text-blue-600 text-xs hover:underline"
                    >
                      Download SARIF
                    </a>
                  </div>
                  <SARIFViewer
                    projectId={projectId}
                    analysisId={analysis.id}
                    resultId={sarifResult.id}
                  />
                </div>
              )}

              {results.filter(
                (r) =>
                  r.result_type !== "sarif" &&
                  r.result_type !== "markdown" &&
                  r.result_type !== "markdown_report" &&
                  r.result_type !== "agent_log",
              ).length > 0 && (
                <div>
                  <h4 className="font-medium text-sm mb-2">Other Artifacts</h4>
                  <div className="space-y-1">
                    {results
                      .filter(
                        (r) =>
                          r.result_type !== "sarif" &&
                          r.result_type !== "markdown" &&
                          r.result_type !== "markdown_report" &&
                          r.result_type !== "agent_log",
                      )
                      .map((r) => (
                        <a
                          key={r.id}
                          href={api.analyses.downloadResult(
                            projectId,
                            analysis.id,
                            r.id,
                          )}
                          className="block p-2 bg-white border rounded hover:bg-gray-50 text-sm"
                        >
                          <span className="font-medium">{r.filename}</span>
                          <span className="text-gray-400 ml-2 text-xs">
                            ({(r.file_size / 1024).toFixed(1)} KB)
                          </span>
                        </a>
                      ))}
                  </div>
                </div>
              )}

              {logResults.length > 0 && (
                <LogSection
                  logs={logResults}
                  projectId={projectId}
                  analysisId={analysis.id}
                />
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ─── log section (archived stdout/stderr) ───────────────────

function LogSection({
  logs,
  projectId,
  analysisId,
}: {
  logs: AnalysisResult[];
  projectId: string;
  analysisId: string;
}) {
  const stdoutLog = logs.find((l) => l.filename === "agent_stdout.log");
  const otherLogs = logs.filter((l) => l.filename !== "agent_stdout.log");
  const [showOther, setShowOther] = useState<string | null>(null);

  return (
    <div className="space-y-2">
      <h4 className="font-medium text-sm">Agent Logs</h4>
      {stdoutLog && (
        <div>
          <div className="flex items-center justify-between mb-1">
            <span className="text-sm font-medium">Output</span>
            <a
              href={api.analyses.downloadResult(projectId, analysisId, stdoutLog.id)}
              className="text-xs text-gray-500 hover:text-blue-600"
            >
              Download
            </a>
          </div>
          <LogContent projectId={projectId} analysisId={analysisId} resultId={stdoutLog.id} />
        </div>
      )}
      {otherLogs.map((log) => (
        <div key={log.id}>
          <div className="flex items-center justify-between">
            <button
              onClick={() => setShowOther(showOther === log.id ? null : log.id)}
              className="text-sm font-medium text-blue-600 hover:underline"
            >
              {showOther === log.id ? "▾" : "▸"} {log.filename}
              <span className="text-gray-400 ml-1 text-xs font-normal">
                ({(log.file_size / 1024).toFixed(1)} KB)
              </span>
            </button>
            <a
              href={api.analyses.downloadResult(projectId, analysisId, log.id)}
              className="text-xs text-gray-500 hover:text-blue-600"
            >
              Download
            </a>
          </div>
          {showOther === log.id && (
            <LogContent projectId={projectId} analysisId={analysisId} resultId={log.id} />
          )}
        </div>
      ))}
    </div>
  );
}

function LogContent({
  projectId,
  analysisId,
  resultId,
}: {
  projectId: string;
  analysisId: string;
  resultId: string;
}) {
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [viewMode, setViewMode] = useState<"formatted" | "raw">("formatted");
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const url = api.analyses.downloadResult(projectId, analysisId, resultId);
    fetch(url, { credentials: "include" })
      .then((r) => {
        if (!r.ok) throw new Error("Failed to load log");
        return r.text();
      })
      .then((text) => setContent(text))
      .catch((err) => setError(err.message));
  }, [projectId, analysisId, resultId]);

  if (error) return <p className="text-sm text-red-600 px-3 pb-2">{error}</p>;
  if (content === null)
    return <p className="text-sm text-gray-500 px-3 pb-2">Loading...</p>;

  const rawLines = content.split("\n");
  const formattedLines = rawLines
    .map(extractStreamMessage)
    .filter((l) => l !== "")
    .flatMap((l) => l.split("\n"));

  return (
    <div>
      <div className="flex justify-end mb-1">
        <div className="inline-flex rounded border border-gray-300 text-xs overflow-hidden">
          <button
            onClick={() => setViewMode("formatted")}
            className={`px-2.5 py-1 ${viewMode === "formatted" ? "bg-gray-800 text-white" : "bg-white text-gray-600 hover:bg-gray-100"}`}
          >
            Formatted
          </button>
          <button
            onClick={() => setViewMode("raw")}
            className={`px-2.5 py-1 border-l border-gray-300 ${viewMode === "raw" ? "bg-gray-800 text-white" : "bg-white text-gray-600 hover:bg-gray-100"}`}
          >
            Raw
          </button>
        </div>
      </div>
      <div
        ref={containerRef}
        className="bg-gray-950 p-3 rounded-lg border border-gray-800 max-h-64 overflow-y-auto overflow-x-hidden space-y-0.5"
      >
        {viewMode === "raw"
          ? rawLines.map((line, i) => (
              <div key={i} className="text-green-400 font-mono text-xs whitespace-pre-wrap break-words">
                {line || "\u00A0"}
              </div>
            ))
          : formattedLines.length > 0
            ? formattedLines.map((line, i) => <StreamLine key={i} line={line} />)
            : rawLines.map((line, i) => (
                <div key={i} className="text-green-400 font-mono text-xs whitespace-pre-wrap break-words">
                  {line || "\u00A0"}
                </div>
              ))}
      </div>
    </div>
  );
}

/** Parse a stream-json line from Claude CLI into a human-readable string.
 * Mirrors the Go extractStreamMessage logic. Returns "" for skipped events. */
function extractStreamMessage(line: string): string {
  line = line.trim();
  if (!line || line[0] !== "{") return "";
  let raw: Record<string, unknown>;
  try {
    raw = JSON.parse(line);
  } catch {
    return "";
  }
  const eventType = raw.type as string;
  if (!eventType) return "";

  switch (eventType) {
    case "assistant":
      return parseAssistantEvent(raw);
    case "user":
      return parseUserEvent(raw);
    case "result": {
      const result = raw.result as string;
      return result ? "[result] " + truncateStr(result, 500) : "";
    }
    case "system": {
      const msg = (raw as { message?: string }).message;
      return msg ? "[system] " + msg : "";
    }
    default:
      return "";
  }
}

function parseAssistantEvent(raw: Record<string, unknown>): string {
  const msg = raw.message as { content?: Array<Record<string, unknown>> };
  if (!msg?.content?.length) return "";
  const parts: string[] = [];
  for (const block of msg.content) {
    switch (block.type) {
      case "text":
        if (block.text) parts.push(block.text as string);
        break;
      case "thinking": {
        const t = block.thinking as string;
        if (t) parts.push("[thinking] " + truncateStr(t, 200));
        break;
      }
      case "tool_use":
        parts.push(formatToolUse(block));
        break;
    }
  }
  return parts.join("\n");
}

function formatToolUse(block: Record<string, unknown>): string {
  const name = block.name as string || "unknown";
  const input = block.input as Record<string, unknown> | undefined;
  if (!input) return `[tool] ${name}`;
  let detail = "";
  switch (name) {
    case "Bash":
      detail = (input.description as string) || truncateStr((input.command as string) || "", 120);
      break;
    case "Read":
    case "Write":
    case "Edit":
      detail = (input.file_path as string) || "";
      break;
    case "Agent":
      detail = (input.description as string) || truncateStr((input.prompt as string) || "", 120);
      break;
    case "WebFetch":
      detail = (input.url as string) || "";
      break;
    default:
      for (const key of ["description", "file_path", "command", "query", "url"]) {
        if (input[key]) { detail = truncateStr(String(input[key]), 120); break; }
      }
  }
  return detail ? `[tool] ${name}: ${detail}` : `[tool] ${name}`;
}

function parseUserEvent(raw: Record<string, unknown>): string {
  const msg = raw.message as { content?: unknown };
  if (!msg?.content) return "";
  const blocks = msg.content as Array<{ type: string; content?: string; is_error?: boolean }>;
  if (!Array.isArray(blocks)) return "";
  for (const b of blocks) {
    if (b.type === "tool_result") {
      const summary = truncateStr(b.content || "", 200);
      if (b.is_error) return "[error] " + summary;
      return summary ? "[result] " + summary : "[result] (ok)";
    }
  }
  return "";
}

function truncateStr(s: string, n: number): string {
  return s.length <= n ? s : s.slice(0, n) + "…";
}

// ─── terminal stream ────────────────────────────────────────

function TerminalStream({ analysisId }: { analysisId: string }) {
  const [lines, setLines] = useState<string[]>([]);
  const [status, setStatus] = useState<"connecting" | "connected" | "error">(
    "connecting",
  );
  const containerRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const retryRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    let cancelled = false;

    function connect() {
      if (cancelled) return;
      setStatus("connecting");
      const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      const ws = new WebSocket(
        `${protocol}//${window.location.host}/ws/analysis/${analysisId}`,
      );
      wsRef.current = ws;

      ws.onopen = () => {
        if (!cancelled) setStatus("connected");
      };

      ws.onmessage = (event) => {
        if (!cancelled) {
          setStatus("connected");
          setLines((prev) => [...prev, event.data]);
        }
      };

      ws.onclose = () => {
        if (!cancelled) {
          // Reconnect after a short delay (analysis may still be running).
          retryRef.current = setTimeout(connect, 3000);
        }
      };

      ws.onerror = () => {
        // onerror is always followed by onclose, which handles reconnection.
        if (!cancelled) setStatus("error");
      };
    }

    connect();

    return () => {
      cancelled = true;
      if (retryRef.current) clearTimeout(retryRef.current);
      if (wsRef.current) wsRef.current.close();
    };
  }, [analysisId]);

  useEffect(() => {
    if (containerRef.current)
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
  }, [lines]);

  return (
    <div>
      <h4 className="font-medium text-sm mb-1">Live Output</h4>
      <div
        ref={containerRef}
        className="bg-gray-950 p-3 rounded-lg border border-gray-800 max-h-64 overflow-y-auto overflow-x-hidden space-y-0.5"
      >
        {lines.length === 0 && (
          <div className="text-gray-500 italic text-sm flex items-center gap-2">
            {status === "connecting" ? (
              <>
                <span className="inline-block w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />
                Connecting to analysis stream...
              </>
            ) : status === "error" ? (
              <>
                <span className="inline-block w-2 h-2 rounded-full bg-red-400 animate-pulse" />
                Waiting for analysis to start... (reconnecting)
              </>
            ) : (
              <>
                <span className="inline-block w-2 h-2 rounded-full bg-green-400 animate-pulse" />
                Connected — waiting for agent output...
              </>
            )}
          </div>
        )}
        {lines.map((line, i) => (
          <StreamLine key={i} line={line} />
        ))}
      </div>
    </div>
  );
}

/** Renders a single line from the WebSocket stream with appropriate styling. */
function StreamLine({ line }: { line: string }) {
  if (line.startsWith("[system]")) {
    return (
      <div className="flex items-start gap-2 py-1 px-2 rounded bg-yellow-950/30 text-yellow-300 text-xs">
        <span className="shrink-0 mt-0.5">⚙</span>
        <span className="break-words whitespace-pre-wrap">{line.slice(9)}</span>
      </div>
    );
  }
  if (line.startsWith("[thinking]")) {
    return (
      <div className="py-1 px-2 text-gray-500 text-xs italic border-l-2 border-gray-700 ml-1 break-words whitespace-pre-wrap">
        💭 {line.slice(11)}
      </div>
    );
  }
  if (line.startsWith("[tool]")) {
    const detail = line.slice(7);
    const colonIdx = detail.indexOf(":");
    const toolName = colonIdx > 0 ? detail.slice(0, colonIdx) : detail;
    const toolDetail = colonIdx > 0 ? detail.slice(colonIdx + 1).trim() : "";
    return (
      <div className="flex items-start gap-2 py-1.5 px-2 rounded bg-cyan-950/30 text-xs">
        <span className="shrink-0 font-mono font-semibold text-cyan-400 bg-cyan-950 px-1.5 py-0.5 rounded text-[10px]">
          {toolName}
        </span>
        {toolDetail && (
          <span className="text-cyan-200/80 break-words whitespace-pre-wrap">{toolDetail}</span>
        )}
      </div>
    );
  }
  if (line.startsWith("[result]")) {
    return (
      <div className="py-1 px-2 text-xs text-gray-400 border-l-2 border-green-800 ml-1 break-words whitespace-pre-wrap font-mono">
        {line.slice(9)}
      </div>
    );
  }
  if (line.startsWith("[error]")) {
    return (
      <div className="flex items-start gap-2 py-1.5 px-2 rounded bg-red-950/30 text-red-300 text-xs">
        <span className="shrink-0">✕</span>
        <span className="break-words whitespace-pre-wrap font-mono">{line.slice(8)}</span>
      </div>
    );
  }
  if (line.startsWith("[stderr]")) {
    return (
      <div className="py-0.5 px-2 text-red-400/70 text-xs font-mono break-words whitespace-pre-wrap">
        {line.slice(9)}
      </div>
    );
  }
  return (
    <div className="py-0.5 px-2 text-gray-200 text-sm break-words whitespace-pre-wrap">
      {line}
    </div>
  );
}

// ─── settings tab ───────────────────────────────────────────

function SettingsTab({
  project,
  groups,
}: {
  project: Project;
  groups?: Group[];
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(project.name);
  const [description, setDescription] = useState(project.description);
  const [readGroupId, setReadGroupId] = useState(project.read_group_id ?? "");
  const [writeGroupId, setWriteGroupId] = useState(
    project.write_group_id ?? "",
  );
  const [adminGroupId, setAdminGroupId] = useState(
    project.admin_group_id ?? "",
  );
  const [confirmDelete, setConfirmDelete] = useState(false);

  const updateMutation = useMutation({
    mutationFn: () =>
      api.projects.update(project.id, {
        name,
        description,
        read_group_id: readGroupId || null,
        write_group_id: writeGroupId || null,
        admin_group_id: adminGroupId || null,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.projects.delete(project.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["projects"] });
    },
  });

  return (
    <div className="space-y-6">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          updateMutation.mutate();
        }}
        className="space-y-4"
      >
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
              Name
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
              className="w-full border rounded px-3 py-2 text-sm"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
              Description
            </label>
            <input
              type="text"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              className="w-full border rounded px-3 py-2 text-sm"
            />
          </div>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <GroupSearch
            label="Read Access Group"
            value={readGroupId}
            groups={groups}
            onChange={setReadGroupId}
          />
          <GroupSearch
            label="Write Access Group"
            value={writeGroupId}
            groups={groups}
            onChange={setWriteGroupId}
          />
          <GroupSearch
            label="Admin Group"
            value={adminGroupId}
            groups={groups}
            onChange={setAdminGroupId}
          />
        </div>

        <div className="flex items-center gap-3">
          <button
            type="submit"
            disabled={updateMutation.isPending}
            className="bg-blue-600 text-white px-4 py-2 rounded text-sm hover:bg-blue-700 disabled:opacity-50"
          >
            {updateMutation.isPending ? "Saving..." : "Save Changes"}
          </button>
          {updateMutation.isSuccess && (
            <span className="text-green-600 text-sm">Saved!</span>
          )}
        </div>
      </form>

      {/* Danger zone */}
      <div className="border-t pt-4">
        <h4 className="text-sm font-semibold text-red-600 mb-2">Danger Zone</h4>
        <p className="text-xs text-gray-500 mb-3">
          Deleting the project removes all packages, analyses, and results.
        </p>
        {confirmDelete ? (
          <div className="flex items-center gap-2">
            <span className="text-sm text-red-600">Are you sure?</span>
            <button
              onClick={() => deleteMutation.mutate()}
              className="text-sm font-medium px-3 py-1.5 rounded bg-red-600 text-white hover:bg-red-700"
            >
              Yes, Delete
            </button>
            <button
              onClick={() => setConfirmDelete(false)}
              className="text-sm text-gray-500 hover:text-gray-700"
            >
              Cancel
            </button>
          </div>
        ) : (
          <button
            onClick={() => setConfirmDelete(true)}
            className="text-sm font-medium px-3 py-1.5 rounded bg-red-100 text-red-800 hover:bg-red-200"
          >
            Delete Project
          </button>
        )}
      </div>
    </div>
  );
}
