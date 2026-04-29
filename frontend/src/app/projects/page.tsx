"use client";

import { useState, useEffect, useRef, useCallback, useMemo } from "react";
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
  type AvailableProvider,
  type ProjectAllowedProvider,
  type DiscoveredModel,
} from "@/lib/api";
import { AnalysisStatus } from "@/components/AnalysisStatus";
import { Pagination, paginate } from "@/components/Pagination";
import { SARIFViewer } from "@/components/SARIFViewer";
import { MarkdownReport } from "@/components/MarkdownReport";
import { ProjectGitHubTab } from "@/components/ProjectGitHubTab";
import { ProjectNRPTab } from "@/components/ProjectNRPTab";
import { NRPLLMKeyInstaller, useNRPLinkSession } from "@/components/NRPLLMKeyInstaller";
import { GitBranchInput, type GitBranchInputHandle } from "@/components/GitBranchInput";
import { FindingsTable } from "@/components/FindingsTable";
import { StreamLine, extractStreamMessage, streamDisplayLines } from "@/lib/stream-utils";

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
          className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 disabled:bg-gray-100 disabled:text-gray-400"
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

  const { data: nrpLinkStatus } = useQuery({
    queryKey: ["nrp-link-status"],
    queryFn: api.nrp.getLinkStatus,
    enabled: session?.authenticated ?? false,
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
              : "bg-brand-600 text-white hover:bg-brand-700"
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
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-brand-500"
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
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-brand-500"
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
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-brand-500"
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
              session={session}
              nrpConfigured={nrpLinkStatus?.oauth_configured ?? false}
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

type ProjectTab = "packages" | "analyses" | "findings" | "github" | "nrp" | "api-keys" | "settings";

function ProjectCard({
  project,
  groups,
  users,
  session,
  nrpConfigured,
  expanded,
  onToggle,
}: {
  project: Project;
  groups?: Group[];
  users?: User[];
  session?: Session;
  nrpConfigured: boolean;
  expanded: boolean;
  onToggle: () => void;
}) {
  const [tab, setTab] = useState<ProjectTab>("packages");
  const isSystemAdmin = session?.roles?.includes("admin") ?? false;

  const canEdit = isSystemAdmin || project.my_role === "write" || project.my_role === "admin";
  const isProjectAdmin = isSystemAdmin || project.my_role === "admin";
  const isOwner = session?.user?.id === project.owner_id;
  const canManageProviders = isSystemAdmin || (isOwner && (session?.roles?.includes("project_creator") ?? false));
  const canDelete = isSystemAdmin || isOwner;

  const groupName = (id: string | null) => {
    if (!id) return null;
    return groups?.find((g) => g.id === id)?.name ?? null;
  };

  const ownerName = () => {
    if (!users) return null;
    const u = users.find((u) => u.id === project.owner_id);
    return u?.display_name || u?.email || null;
  };

  const tabs: { key: ProjectTab; label: string }[] = [
    { key: "packages", label: "Packages" },
    { key: "analyses", label: "Analyses" },
    { key: "findings", label: "Findings" },
    ...(canEdit ? [{ key: "github" as ProjectTab, label: "GitHub" }] : []),
    ...(nrpConfigured ? [{ key: "nrp" as ProjectTab, label: "NRP" }] : []),
    ...(isProjectAdmin ? [{ key: "api-keys" as ProjectTab, label: "LLMs" }] : []),
    ...(canEdit ? [{ key: "settings" as ProjectTab, label: "Settings" }] : []),
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
          className="p-1 text-gray-400 hover:text-brand-600 flex-shrink-0"
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
                      ? "border-brand-600 text-brand-600"
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
            {tab === "findings" && <FindingsTabInline projectId={project.id} projectName={project.name} canEdit={canEdit} />}
            {tab === "github" && canEdit && <ProjectGitHubTab projectId={project.id} canManageInstallations={isProjectAdmin} />}
            {tab === "nrp" && nrpConfigured && <ProjectNRPTab projectId={project.id} isSystemAdmin={isSystemAdmin} isProjectAdmin={isProjectAdmin} />}
            {tab === "api-keys" && isProjectAdmin && <ProviderKeysTab projectId={project.id} />}
            {tab === "settings" && canEdit && (
              <SettingsTab
                project={project}
                groups={groups}
                canManageProviders={canManageProviders}
                canDelete={canDelete}
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
  const [sarifUploadEnabled, setSarifUploadEnabled] = useState(false);
  const [githubSyncEnabled, setGitHubSyncEnabled] = useState(false);
  const [webhookPushEnabled, setWebhookPushEnabled] = useState(false);
  const [webhookPREnabled, setWebhookPREnabled] = useState(false);
  const [branchError, setBranchError] = useState<string | null>(null);
  const [installationWarning, setInstallationWarning] = useState<string | null>(null);
  const [installationSuccess, setInstallationSuccess] = useState<string | null>(null);
  const [accessInstallURL, setAccessInstallURL] = useState<string | null>(null);
  const [needsGitHubLink, setNeedsGitHubLink] = useState(false);
  const [linkingGitHub, setLinkingGitHub] = useState(false);
  const [linkToProjectInstallationID, setLinkToProjectInstallationID] = useState<number | null>(null);
  const [linkToProjectError, setLinkToProjectError] = useState<string | null>(null);

  // Parse owner/repo from current gitUrl for warning messages.
  const parsedGitHub = useMemo(() => {
    const m = gitUrl.match(/github\.com[/:]([^/]+)\/([^/.]+?)(?:\.git)?\/?$/i);
    return m ? { owner: m[1], repo: m[2] } : null;
  }, [gitUrl]);

  const { data: projectInstallations } = useQuery({
    queryKey: ["project-github-installations", projectId],
    queryFn: async () => {
      try {
        const resp = await api.github.listProjectInstallations(projectId);
        return resp.installations ?? [];
      } catch {
        return [];
      }
    },
  });
  const linkedProjectInstallationIDs = useMemo(
    () =>
      new Set(
        (projectInstallations ?? [])
          .filter((inst) => !!inst.linked_to_project)
          .map((inst) => inst.installation_id),
      ),
    [projectInstallations],
  );

  const formatInstallationRef = useCallback(
    (
      installationID: number,
      fallbackAccount: string,
      access?: {
        installation_account_login?: string;
        matched_installations?: Array<{ installation_id: number; account_login: string }>;
      },
    ) => {
      const account =
        access?.installation_account_login ||
        access?.matched_installations?.find((inst) => inst.installation_id === installationID)?.account_login ||
        fallbackAccount;
      return `${account} (${installationID})`;
    },
    [],
  );

  const setConnectedRepoMessage = useCallback((owner: string, repo: string) => {
    setInstallationSuccess(
      `Connected to ${owner}/${repo} via GitHub App install — results will be uploaded to GitHub Code Scanning.`,
    );
  }, []);

  const linkProjectInstallationMutation = useMutation({
    mutationFn: (installationId: number) => api.github.addProjectInstallation(projectId, installationId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["project-github-installations", projectId] });
      setLinkToProjectError(null);
      setLinkToProjectInstallationID(null);
      if (parsedGitHub && sarifUploadEnabled) {
        setInstallationWarning(null);
        setConnectedRepoMessage(parsedGitHub.owner, parsedGitHub.repo);
      }
    },
    onError: (error: unknown) => {
      setLinkToProjectError(error instanceof Error ? error.message : "Failed to link GitHub App install to project.");
    },
  });

  const handleBranchDetection = useCallback((result: { ok: boolean; error?: string }) => {
    if (result.ok) {
      setBranchError(null);
      setNeedsGitHubLink(false);
      setLinkToProjectInstallationID(null);
      setLinkToProjectError(null);
      return;
    }
    if (parsedGitHub) {
      api.github.userRepoAccess(parsedGitHub.owner, parsedGitHub.repo, projectId).then((access) => {
        if (access.accessible) {
          const installationID = access.installation_id ?? 0;
          if (installationID > 0 && !linkedProjectInstallationIDs.has(installationID)) {
            setLinkToProjectInstallationID(installationID);
            setLinkToProjectError(null);
            setInstallationWarning(
              `GitHub App install ${formatInstallationRef(installationID, parsedGitHub.owner, access)} can access ${parsedGitHub.owner}/${parsedGitHub.repo}. Click \"Link to project\" to enable package access in this project.`,
            );
            setInstallationSuccess(null);
          } else {
            setLinkToProjectInstallationID(null);
          }
          setBranchError(result.error ?? 'Could not detect branches.');
        } else if (access.needs_link && !access.has_installation) {
          setBranchError('Could not access this repository — link your GitHub account to check access.');
          setNeedsGitHubLink(true);
          setAccessInstallURL(null);
          setLinkToProjectInstallationID(null);
        } else if (!access.has_installation) {
          setBranchError('Could not access this repository — it may be private.');
          setAccessInstallURL(access.install_url ?? null);
          setNeedsGitHubLink(false);
          setLinkToProjectInstallationID(null);
        } else {
          setBranchError(access.error ?? 'GitHub App does not have access to this repository.');
          setAccessInstallURL(null);
          setNeedsGitHubLink(false);
          setLinkToProjectInstallationID(null);
        }
      }).catch(() => {
        setLinkToProjectInstallationID(null);
        setBranchError(result.error ?? 'Could not detect branches.');
      });
    } else {
      setLinkToProjectInstallationID(null);
      setBranchError(result.error ?? 'Could not detect branches.');
    }
  }, [formatInstallationRef, linkedProjectInstallationIDs, parsedGitHub, projectId]);

  const handleSarifToggle = useCallback(async (checked: boolean) => {
    setSarifUploadEnabled(checked);
    setInstallationWarning(null);
    setInstallationSuccess(null);
    setAccessInstallURL(null);
    setLinkToProjectInstallationID(null);
    setLinkToProjectError(null);
    if (!checked || !parsedGitHub) return;
    try {
      const access = await api.github.userRepoAccess(parsedGitHub.owner, parsedGitHub.repo, projectId);
      if (access.accessible) {
        const installationID = access.installation_id ?? 0;
        if (installationID > 0 && !linkedProjectInstallationIDs.has(installationID)) {
          setLinkToProjectInstallationID(installationID);
          setInstallationWarning(
            `GitHub App install ${formatInstallationRef(installationID, parsedGitHub.owner, access)} can access ${parsedGitHub.owner}/${parsedGitHub.repo}. Click \"Link to project\" to enable package access in this project.`,
          );
        } else {
          setConnectedRepoMessage(parsedGitHub.owner, parsedGitHub.repo);
        }
      } else if (!access.has_installation) {
        setInstallationWarning(
          `No GitHub App installation found for "${parsedGitHub.owner}". Results won't be uploaded until the app is installed.`
        );
        setAccessInstallURL(access.install_url ?? null);
      } else if (access.needs_link && !access.has_installation) {
        setInstallationWarning('Link your GitHub account to enable SARIF upload.');
        setNeedsGitHubLink(true);
      } else if (!access.accessible) {
        setInstallationWarning(
          access.error ?? `GitHub App does not have access to this repository.`
        );
      }
    } catch {
      // If the probe fails, don't block — the user can still enable it.
    }
  }, [formatInstallationRef, linkedProjectInstallationIDs, parsedGitHub, projectId, setConnectedRepoMessage]);

  // Re-check repo access when the user returns from the GitHub App install page.
  const branchInputRef = useRef<GitBranchInputHandle>(null);
  useEffect(() => {
    if (!parsedGitHub || (!branchError && !installationWarning)) return;

    const recheckAccess = () => {
      api.github.userRepoAccess(parsedGitHub.owner, parsedGitHub.repo, projectId).then((access) => {
        if (access.accessible) {
          setBranchError(null);
          const installationID = access.installation_id ?? 0;
          if (installationID > 0 && !linkedProjectInstallationIDs.has(installationID)) {
            setInstallationWarning(
              `GitHub App install ${formatInstallationRef(installationID, parsedGitHub.owner, access)} can access ${parsedGitHub.owner}/${parsedGitHub.repo}. Click \"Link to project\" to enable package access in this project.`,
            );
            setInstallationSuccess(null);
            setLinkToProjectInstallationID(installationID);
          } else {
            setInstallationWarning(null);
            setLinkToProjectInstallationID(null);
          }
          setAccessInstallURL(null);
          setNeedsGitHubLink(false);
          if (sarifUploadEnabled) {
            setConnectedRepoMessage(parsedGitHub.owner, parsedGitHub.repo);
          }
          branchInputRef.current?.refetch();
        } else if (!access.needs_link) {
          setNeedsGitHubLink(false);
          setLinkToProjectInstallationID(null);
        }
      }).catch(() => { /* ignore */ });
    };

    const onVisible = () => {
      if (document.visibilityState === 'visible') recheckAccess();
    };
    const onMessage = (e: MessageEvent) => {
      if (e.origin === window.location.origin && e.data?.type === 'github-linked') {
        setLinkingGitHub(false);
        setNeedsGitHubLink(false);
        recheckAccess();
      }
    };
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener('message', onMessage);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('message', onMessage);
    };
  }, [formatInstallationRef, linkedProjectInstallationIDs, parsedGitHub, branchError, installationWarning, sarifUploadEnabled, setConnectedRepoMessage]);

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
        sarif_upload_enabled: sarifUploadEnabled,
        github_sync_enabled: githubSyncEnabled,
        webhook_push_enabled: webhookPushEnabled,
        webhook_pr_enabled: webhookPREnabled,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["packages", projectId] });
      setAdding(false);
      setName("");
      setGitUrl("");
      setGitBranch("main");
      setAnalysisPrompt("");
      setSarifUploadEnabled(false);
      setGitHubSyncEnabled(false);
      setWebhookPushEnabled(false);
      setWebhookPREnabled(false);
      setBranchError(null);
      setInstallationWarning(null);
      setInstallationSuccess(null);
      setLinkToProjectInstallationID(null);
      setLinkToProjectError(null);
    },
  });

  const updateMutation = useMutation({
    mutationFn: (pkgId: string) =>
      api.packages.update(projectId, pkgId, {
        name,
        git_url: gitUrl,
        git_branch: gitBranch,
        analysis_prompt: analysisPrompt,
        sarif_upload_enabled: sarifUploadEnabled,
        github_sync_enabled: githubSyncEnabled,
        webhook_push_enabled: webhookPushEnabled,
        webhook_pr_enabled: webhookPREnabled,
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
    setSarifUploadEnabled(pkg.sarif_upload_enabled ?? false);
    setGitHubSyncEnabled(pkg.github_sync_enabled ?? false);
    setWebhookPushEnabled(pkg.webhook_push_enabled ?? false);
    setWebhookPREnabled(pkg.webhook_pr_enabled ?? false);
    setAdding(false);
    setBranchError(null);
    setInstallationWarning(null);
    setInstallationSuccess(null);
    setLinkToProjectInstallationID(null);
    setLinkToProjectError(null);
    // Auto-check access when editing a package with SARIF already enabled
    if (pkg.sarif_upload_enabled) {
      const m = pkg.git_url.match(/github\.com[/:]([^/]+)\/([^/.]+?)(?:\.git)?\/?$/i);
      if (m) {
        api.github.userRepoAccess(m[1], m[2], projectId).then((access) => {
          if (access.accessible && access.has_installation) {
            const installationID = access.installation_id ?? 0;
            if (installationID > 0 && !linkedProjectInstallationIDs.has(installationID)) {
              setLinkToProjectInstallationID(installationID);
              setInstallationWarning(
                `GitHub App install ${formatInstallationRef(installationID, m[1], access)} can access ${m[1]}/${m[2]}. Click \"Link to project\" to enable package access in this project.`,
              );
            } else {
              setConnectedRepoMessage(m[1], m[2]);
            }
          }
        }).catch(() => { /* ignore */ });
      }
    }
  };

  const cancelEdit = () => {
    setEditingId(null);
    setName("");
    setGitUrl("");
    setGitBranch("main");
    setAnalysisPrompt("");
    setSarifUploadEnabled(false);
    setGitHubSyncEnabled(false);
    setWebhookPushEnabled(false);
    setWebhookPREnabled(false);
    setBranchError(null);
    setInstallationWarning(null);
    setInstallationSuccess(null);
    setLinkToProjectInstallationID(null);
    setLinkToProjectError(null);
  };

  const handleGitHubLink = async () => {
    setLinkingGitHub(true);
    try {
      const resp = await api.github.startLink();
      window.open(resp.authorize_url, 'github-link', 'width=600,height=700');
    } catch {
      setLinkingGitHub(false);
    }
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
            setBranchError(null);
            setInstallationWarning(null);
            setLinkToProjectInstallationID(null);
            setLinkToProjectError(null);
            if (!adding) {
              setName("");
              setGitUrl("");
              setGitBranch("main");
              setAnalysisPrompt("");
              setSarifUploadEnabled(false);
              setGitHubSyncEnabled(false);
              setWebhookPushEnabled(false);
              setWebhookPREnabled(false);
            }
          }}
          className={`px-3 py-1.5 text-sm rounded font-medium transition-colors ${
            adding
              ? "bg-gray-200 text-gray-700 hover:bg-gray-300"
              : "bg-brand-600 text-white hover:bg-brand-700"
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
            {parsedGitHub && branchError && (
              <p className="text-xs text-amber-600 mt-1">
                {branchError}
                {needsGitHubLink && (
                  <>{' '}
                    <button
                      type="button"
                      onClick={handleGitHubLink}
                      disabled={linkingGitHub}
                      className="underline text-brand-600 hover:text-brand-700"
                    >
                      {linkingGitHub ? 'Linking…' : 'Link your GitHub account'}
                    </button>
                  </>
                )}
                {!needsGitHubLink && accessInstallURL && (
                  <>{' '}
                    <a href={accessInstallURL} target="_blank" rel="noopener noreferrer" className="underline text-brand-600 hover:text-brand-700">
                      Install the GitHub App
                    </a>{' '}
                    on <strong>{parsedGitHub.owner}</strong> to grant access.
                  </>
                )}
                {linkToProjectInstallationID && (
                  <>{' '}
                    <button
                      type="button"
                      onClick={() => {
                        setLinkToProjectError(null);
                        linkProjectInstallationMutation.mutate(linkToProjectInstallationID);
                      }}
                      disabled={linkProjectInstallationMutation.isPending}
                      className="underline text-brand-600 hover:text-brand-700"
                    >
                      {linkProjectInstallationMutation.isPending ? 'Linking to project...' : 'Link to project'}
                    </button>
                  </>
                )}
              </p>
            )}
            {linkToProjectError && (
              <p className="text-xs text-red-600 mt-1">{linkToProjectError}</p>
            )}
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
              ref={branchInputRef}
              gitUrl={gitUrl}
              value={gitBranch}
              onChange={setGitBranch}
              labelClassName="block text-xs font-medium text-gray-500 mb-1"
              onDetectionResult={handleBranchDetection}
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
          <div>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={sarifUploadEnabled}
                onChange={(e) => handleSarifToggle(e.target.checked)}
                className="rounded"
              />
              <span>Upload results to GitHub Code Scanning</span>
            </label>
            <label className="flex items-center gap-2 text-sm mt-2">
              <input
                type="checkbox"
                checked={githubSyncEnabled}
                onChange={(e) => setGitHubSyncEnabled(e.target.checked)}
                className="rounded"
              />
              <span>Sync GitHub alert status back into SWAMP</span>
            </label>
            <label className="flex items-center gap-2 text-sm mt-2">
              <input
                type="checkbox"
                checked={webhookPushEnabled}
                onChange={(e) => setWebhookPushEnabled(e.target.checked)}
                className="rounded"
              />
              <span>Trigger analysis on pushes to this package branch</span>
            </label>
            <label className="flex items-center gap-2 text-sm mt-2">
              <input
                type="checkbox"
                checked={webhookPREnabled}
                onChange={(e) => setWebhookPREnabled(e.target.checked)}
                className="rounded"
              />
              <span>Trigger analysis on pull request updates targeting this branch</span>
            </label>
            {installationWarning && (
              <p className="text-xs text-amber-600 mt-1">
                {installationWarning}
                {needsGitHubLink && (
                  <>{' '}
                    <button
                      type="button"
                      onClick={handleGitHubLink}
                      disabled={linkingGitHub}
                      className="underline text-brand-600 hover:text-brand-700"
                    >
                      {linkingGitHub ? 'Linking…' : 'Link your GitHub account'}
                    </button>
                  </>
                )}
                {!needsGitHubLink && accessInstallURL && (
                  <>{' '}
                    <a href={accessInstallURL} target="_blank" rel="noopener noreferrer" className="underline text-brand-600 hover:text-brand-700">
                      Install the GitHub App
                    </a>{' '}
                    to enable uploads.
                  </>
                )}
                {linkToProjectInstallationID && (
                  <>{' '}
                    <button
                      type="button"
                      onClick={() => {
                        setLinkToProjectError(null);
                        linkProjectInstallationMutation.mutate(linkToProjectInstallationID);
                      }}
                      disabled={linkProjectInstallationMutation.isPending}
                      className="underline text-brand-600 hover:text-brand-700"
                    >
                      {linkProjectInstallationMutation.isPending ? 'Linking to project...' : 'Link to project'}
                    </button>
                  </>
                )}
              </p>
            )}
            {linkToProjectError && (
              <p className="text-xs text-red-600 mt-1">{linkToProjectError}</p>
            )}
            {!installationWarning && installationSuccess && (
              <p className="text-xs text-green-600 mt-1 flex items-center gap-1">
                <svg className="h-3.5 w-3.5 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20"><path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clipRule="evenodd" /></svg>
                {installationSuccess}
              </p>
            )}
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
                  {parsedGitHub && branchError && (
                    <p className="text-xs text-amber-600 mt-1">
                      {branchError}
                      {needsGitHubLink && (
                        <>{' '}
                          <button
                            type="button"
                            onClick={handleGitHubLink}
                            disabled={linkingGitHub}
                            className="underline text-brand-600 hover:text-brand-700"
                          >
                            {linkingGitHub ? 'Linking…' : 'Link your GitHub account'}
                          </button>
                        </>
                      )}
                      {!needsGitHubLink && accessInstallURL && (
                        <>{' '}
                          <a href={accessInstallURL} target="_blank" rel="noopener noreferrer" className="underline text-brand-600 hover:text-brand-700">
                            Install the GitHub App
                          </a>{' '}
                          on <strong>{parsedGitHub.owner}</strong> to grant access.
                        </>
                      )}
                      {linkToProjectInstallationID && (
                        <>{' '}
                          <button
                            type="button"
                            onClick={() => {
                              setLinkToProjectError(null);
                              linkProjectInstallationMutation.mutate(linkToProjectInstallationID);
                            }}
                            disabled={linkProjectInstallationMutation.isPending}
                            className="underline text-brand-600 hover:text-brand-700"
                          >
                            {linkProjectInstallationMutation.isPending ? 'Linking to project...' : 'Link to project'}
                          </button>
                        </>
                      )}
                    </p>
                  )}
                  {linkToProjectError && (
                    <p className="text-xs text-red-600 mt-1">{linkToProjectError}</p>
                  )}
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
                    ref={branchInputRef}
                    gitUrl={gitUrl}
                    value={gitBranch}
                    onChange={setGitBranch}
                    labelClassName="block text-xs font-medium text-gray-500 mb-1"
                    projectId={projectId}
                    packageId={pkg.id}
                    onDetectionResult={handleBranchDetection}
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
                <div>
                  <label className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={sarifUploadEnabled}
                      onChange={(e) => handleSarifToggle(e.target.checked)}
                      className="rounded"
                    />
                    <span>Upload results to GitHub Code Scanning</span>
                  </label>
                  <label className="flex items-center gap-2 text-sm mt-2">
                    <input
                      type="checkbox"
                      checked={githubSyncEnabled}
                      onChange={(e) => setGitHubSyncEnabled(e.target.checked)}
                      className="rounded"
                    />
                    <span>Sync GitHub alert status back into SWAMP</span>
                  </label>
                  <label className="flex items-center gap-2 text-sm mt-2">
                    <input
                      type="checkbox"
                      checked={webhookPushEnabled}
                      onChange={(e) => setWebhookPushEnabled(e.target.checked)}
                      className="rounded"
                    />
                    <span>Trigger analysis on pushes to this package branch</span>
                  </label>
                  <label className="flex items-center gap-2 text-sm mt-2">
                    <input
                      type="checkbox"
                      checked={webhookPREnabled}
                      onChange={(e) => setWebhookPREnabled(e.target.checked)}
                      className="rounded"
                    />
                    <span>Trigger analysis on pull request updates targeting this branch</span>
                  </label>
                  {installationWarning && (
                    <p className="text-xs text-amber-600 mt-1">
                      {installationWarning}
                      {needsGitHubLink && (
                        <>{' '}
                          <button
                            type="button"
                            onClick={handleGitHubLink}
                            disabled={linkingGitHub}
                            className="underline text-brand-600 hover:text-brand-700"
                          >
                            {linkingGitHub ? 'Linking…' : 'Link your GitHub account'}
                          </button>
                        </>
                      )}
                      {!needsGitHubLink && accessInstallURL && (
                        <>{' '}
                          <a href={accessInstallURL} target="_blank" rel="noopener noreferrer" className="underline text-brand-600 hover:text-brand-700">
                            Install the GitHub App
                          </a>{' '}
                          to enable uploads.
                        </>
                      )}
                      {linkToProjectInstallationID && (
                        <>{' '}
                          <button
                            type="button"
                            onClick={() => {
                              setLinkToProjectError(null);
                              linkProjectInstallationMutation.mutate(linkToProjectInstallationID);
                            }}
                            disabled={linkProjectInstallationMutation.isPending}
                            className="underline text-brand-600 hover:text-brand-700"
                          >
                            {linkProjectInstallationMutation.isPending ? 'Linking to project...' : 'Link to project'}
                          </button>
                        </>
                      )}
                    </p>
                  )}
                  {linkToProjectError && (
                    <p className="text-xs text-red-600 mt-1">{linkToProjectError}</p>
                  )}
                  {!installationWarning && installationSuccess && (
                    <p className="text-xs text-green-600 mt-1 flex items-center gap-1">
                      <svg className="h-3.5 w-3.5 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20"><path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clipRule="evenodd" /></svg>
                      {installationSuccess}
                    </p>
                  )}
                </div>
                <div className="flex gap-2">
                  <button
                    type="submit"
                    disabled={updateMutation.isPending}
                    className="bg-brand-600 text-white px-3 py-1.5 text-sm rounded hover:bg-brand-700 disabled:opacity-50"
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
                  {pkg.github_owner && pkg.github_repo && (
                    <div className="text-xs text-gray-400 mt-0.5">
                      GitHub: {pkg.github_owner}/{pkg.github_repo}
                      {pkg.sarif_upload_enabled && (
                        <span className="text-green-600"> · Code Scanning enabled</span>
                      )}
                      {pkg.github_sync_enabled && (
                        <span className="text-green-600"> · Alert sync enabled</span>
                      )}
                      {pkg.webhook_push_enabled && (
                        <span className="text-green-600"> · Push trigger enabled</span>
                      )}
                      {pkg.webhook_pr_enabled && (
                        <span className="text-green-600"> · PR trigger enabled</span>
                      )}
                    </div>
                  )}
                  {pkg.analysis_prompt && (
                    <div className="text-xs text-gray-400 mt-0.5 italic">
                      Prompt: {pkg.analysis_prompt.length > 80 ? pkg.analysis_prompt.slice(0, 80) + "…" : pkg.analysis_prompt}
                    </div>
                  )}
                </div>
                <div className="flex gap-2">
                  <button
                    onClick={() => startEdit(pkg)}
                    className="text-brand-500 text-xs hover:text-brand-700"
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
  const [agentModel, setAgentModel] = useState("");
  const [selectedProvider, setSelectedProvider] = useState("");
  const [openAnalysis, setOpenAnalysis] = useState<string | null>(null);
  const [analysisPage, setAnalysisPage] = useState(1);

  // Fetch available providers (global + project)
  const { data: availableProviders } = useQuery({
    queryKey: ['available-providers', projectId],
    queryFn: () => api.availableProviders(projectId),
    staleTime: 60_000,
  });

  // Auto-select the first available provider when none is explicitly chosen.
  const effectiveProvider = selectedProvider
    || (availableProviders?.length ? `${availableProviders[0].source}:${availableProviders[0].id}` : '');

  const { data: agentStatus } = useQuery({
    queryKey: ['agent-status'],
    queryFn: () => api.agent.status(),
    staleTime: 60_000,
  });

  const hasProviders = availableProviders && availableProviders.length > 0;

  // Parse selected provider
  const selectedProviderObj = availableProviders?.find(
    (p) => `${p.source}:${p.id}` === effectiveProvider
  );

  // Discover models for selected provider
  const { data: discoveredModels, isFetching: loadingModels } = useQuery({
    queryKey: ['discovered-models', effectiveProvider],
    queryFn: () => {
      if (!selectedProviderObj) return Promise.resolve([]);
      if (selectedProviderObj.source === 'global' || selectedProviderObj.source === 'env') {
        return api.discoverAvailableProviderModels(projectId, selectedProviderObj.source, selectedProviderObj.id);
      }
      return api.providerKeys.discoverModels(projectId, selectedProviderObj.id);
    },
    enabled: !!selectedProviderObj,
    staleTime: 5 * 60_000,
  });

  const agentReady = hasProviders || agentStatus?.ready;

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

  // When there's exactly one package, skip the checkbox UI and use it
  // implicitly. Otherwise the user picks from the checkbox list.
  const singlePackage = packages && packages.length === 1 ? packages[0] : null;
  const effectivePkgs = singlePackage ? [singlePackage.id] : selectedPkgs;

  const triggerMutation = useMutation({
    mutationFn: () => {
      // Resolve concrete model: user selection → provider default → first discovered model.
      const effectiveModel = agentModel || selectedProviderObj?.default_model || discoveredModels?.[0]?.id || undefined;
      const data: { package_ids: string[]; agent_model?: string; custom_prompt?: string; provider_id?: string; provider_source?: string } = {
        package_ids: effectivePkgs,
        agent_model: effectiveModel,
        custom_prompt: customPrompt || undefined,
      };
      if (selectedProviderObj) {
        data.provider_id = selectedProviderObj.id;
        data.provider_source = selectedProviderObj.source;
      }
      return api.analyses.create(projectId, data);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["analyses", projectId] });
      setSelectedPkgs([]);
      setCustomPrompt("");
      setAgentModel("");
    },
  });

  return (
    <div>
      {/* Trigger new analysis */}
      {packages && packages.length > 0 && (
        <div className="bg-white p-4 rounded border mb-4">
          <h3 className="text-sm font-medium mb-2">Run New Analysis</h3>
          {!agentReady && (
            <div className="text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded p-2 mb-3">
              No LLM providers are configured. Ask an admin to add a provider in Settings, or set <code className="bg-amber-100 px-1 rounded">AGENT_API_KEY</code>.
            </div>
          )}
          {triggerMutation.isError && (
            <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded p-2 mb-3">
              {triggerMutation.error?.message || 'Failed to start analysis'}
            </div>
          )}
          {singlePackage ? (
            <p className="text-sm text-gray-700 mb-3">
              Package: <span className="font-medium">{singlePackage.name}</span>{' '}
              <span className="text-gray-500">({singlePackage.git_branch})</span>
            </p>
          ) : (
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
          )}

          {/* Provider selection */}
          {hasProviders ? (
            <>
              <div className="mb-3">
                <label className="block text-xs font-medium text-gray-500 mb-1">
                  Provider
                </label>
                <select
                  value={effectiveProvider}
                  onChange={(e) => {
                    setSelectedProvider(e.target.value);
                    setAgentModel("");
                  }}
                  className="w-full border rounded px-3 py-2 text-sm bg-white"
                >
                  {availableProviders.map((p) => (
                    <option key={`${p.source}:${p.id}`} value={`${p.source}:${p.id}`}>
                      {p.label} ({p.api_schema}){p.source === 'project' ? ' — project' : p.source === 'env' ? ' — env' : ''}
                    </option>
                  ))}
                </select>
              </div>
              <div className="mb-3">
                <label className="block text-xs font-medium text-gray-500 mb-1">
                  Model
                </label>
                {loadingModels ? (
                  <p className="text-xs text-gray-500 py-2">Discovering models...</p>
                ) : discoveredModels && discoveredModels.length > 0 ? (
                  <select
                    value={agentModel}
                    onChange={(e) => setAgentModel(e.target.value)}
                    className="w-full border rounded px-3 py-2 text-sm bg-white"
                  >
                    <option value="">
                      {selectedProviderObj?.default_model
                        ? `Default (${selectedProviderObj.default_model})`
                        : 'Auto (provider default)'}
                    </option>
                    {discoveredModels.map((m: DiscoveredModel) => (
                      <option key={m.id} value={m.id}>
                        {m.display_name || m.id}
                      </option>
                    ))}
                  </select>
                ) : (
                  <>
                    <input
                      type="text"
                      value={agentModel}
                      onChange={(e) => setAgentModel(e.target.value)}
                      placeholder={selectedProviderObj?.default_model || 'Auto (provider default)'}
                      className="w-full border rounded px-3 py-2 text-sm bg-white"
                    />
                    <p className="text-xs text-gray-500 mt-1">
                      {effectiveProvider ? 'Could not discover models. Enter a model ID manually or leave blank.' : 'Select a provider to discover available models.'}
                    </p>
                  </>
                )}
              </div>
            </>
          ) : (
            /* No providers available for this project */
            <div className="mb-3 text-sm text-amber-700 bg-amber-50 border border-amber-200 rounded p-3">
              No LLM providers are available for this project. An admin must allow providers for this project in the <strong>Settings → Provider Access</strong> tab.
            </div>
          )}

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
            disabled={!effectivePkgs.length || triggerMutation.isPending || !agentReady || (hasProviders && !selectedProviderObj)}
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
      (analysis.status === "completed" || analysis.status === "failed" || analysis.status === "timed_out"),
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
              <svg className="w-3 h-3 animate-spin text-brand-500" viewBox="0 0 24 24" fill="none">
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
            className="p-1 text-gray-400 hover:text-brand-600"
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
                  {analysis.status === "cancelled" ? "Cancelled" : analysis.status === "timed_out" ? "Timed Out" : "Completed"}
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
                      className="text-brand-600 text-xs hover:underline"
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
                      className="text-brand-600 text-xs hover:underline"
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
              className="text-xs text-gray-500 hover:text-brand-600"
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
              className="text-sm font-medium text-brand-600 hover:underline"
            >
              {showOther === log.id ? "▾" : "▸"} {log.filename}
              <span className="text-gray-400 ml-1 text-xs font-normal">
                ({(log.file_size / 1024).toFixed(1)} KB)
              </span>
            </button>
            <a
              href={api.analyses.downloadResult(projectId, analysisId, log.id)}
              className="text-xs text-gray-500 hover:text-brand-600"
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

  const displayLines = useMemo(() => streamDisplayLines(lines), [lines]);

  return (
    <div>
      <h4 className="font-medium text-sm mb-1">Live Output</h4>
      <div
        ref={containerRef}
        className="bg-gray-950 p-3 rounded-lg border border-gray-800 max-h-64 overflow-y-auto overflow-x-hidden space-y-0.5"
      >
        {displayLines.length === 0 && (
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
        {displayLines.map((line, i) => (
          <StreamLine key={i} line={line} />
        ))}
      </div>
    </div>
  );
}

// ─── settings tab ───────────────────────────────────────────

function SettingsTab({
  project,
  groups,
  canManageProviders,
  canDelete,
}: {
  project: Project;
  groups?: Group[];
  canManageProviders?: boolean;
  canDelete?: boolean;
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

  // All enabled global/env providers (unfiltered)
  const { data: allProviders } = useQuery({
    queryKey: ['all-providers', project.id],
    queryFn: () => api.allProviders(project.id),
    enabled: !!canManageProviders,
  });

  // Currently allowed providers for this project
  const { data: allowedProviders } = useQuery({
    queryKey: ['allowed-providers', project.id],
    queryFn: () => api.allowedProviders.list(project.id),
    enabled: !!canManageProviders,
  });

  const allowedSet = new Set(
    (allowedProviders ?? []).map((a: ProjectAllowedProvider) => `${a.provider_source}:${a.provider_id}`)
  );

  const toggleProviderMut = useMutation({
    mutationFn: ({ providerId, providerSource, allowed }: { providerId: string; providerSource: string; allowed: boolean }) => {
      if (allowed) {
        return api.allowedProviders.remove(project.id, providerId, providerSource);
      }
      return api.allowedProviders.add(project.id, providerId, providerSource);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['allowed-providers', project.id] });
      queryClient.invalidateQueries({ queryKey: ['available-providers', project.id] });
    },
  });

  // Filter to only env and global providers (not project keys)
  const systemProviders = (allProviders ?? []).filter((p: AvailableProvider) => p.source === 'env' || p.source === 'global');

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
            className="bg-brand-600 text-white px-4 py-2 rounded text-sm hover:bg-brand-700 disabled:opacity-50"
          >
            {updateMutation.isPending ? "Saving..." : "Save Changes"}
          </button>
          {updateMutation.isSuccess && (
            <span className="text-green-600 text-sm">Saved!</span>
          )}
        </div>
      </form>

      {/* Provider Access — only visible to admins/project_creators */}
      {canManageProviders && (
        <div className="border-t pt-4">
          <h4 className="text-sm font-semibold mb-2">Provider Access</h4>
          <p className="text-xs text-gray-500 mb-2">
            Control which global and environment providers this project can use.
          </p>
          {systemProviders.length > 0 ? (
            <div className="border rounded-md divide-y">
              {systemProviders.map((p: AvailableProvider) => {
                const key = `${p.source}:${p.id}`;
                const isAllowed = allowedSet.has(key);
                return (
                  <div key={key} className="p-2 flex items-center justify-between">
                    <div className="flex items-center gap-2 min-w-0">
                      <span className="text-sm">{p.label}</span>
                      <span className={`px-1.5 py-0.5 text-xs rounded ${
                        p.api_schema === 'anthropic' ? 'bg-purple-100 text-purple-700' : 'bg-green-100 text-green-700'
                      }`}>
                        {p.api_schema}
                      </span>
                      <span className={`px-1.5 py-0.5 text-xs rounded ${
                        p.source === 'env' ? 'bg-blue-100 text-blue-700' : 'bg-gray-100 text-gray-600'
                      }`}>
                        {p.source}
                      </span>
                    </div>
                    <button
                      onClick={() => toggleProviderMut.mutate({
                        providerId: p.id,
                        providerSource: p.source,
                        allowed: isAllowed,
                      })}
                      disabled={toggleProviderMut.isPending}
                      className={`px-2 py-1 text-xs rounded ${
                        isAllowed
                          ? 'bg-green-100 text-green-700 hover:bg-green-200'
                          : 'bg-gray-100 text-gray-500 hover:bg-gray-200'
                      }`}
                    >
                      {isAllowed ? 'Allowed' : 'Not Allowed'}
                    </button>
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="text-xs text-gray-400 text-center py-3 border rounded-md">
              No global or environment providers configured. Add providers in{' '}
              <a href="/admin/settings" className="text-brand-600 hover:underline">Admin Settings</a>.
            </div>
          )}
        </div>
      )}

      {/* Danger zone */}
      {canDelete && (
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
      )}
    </div>
  );
}

function FindingsTabInline({
  projectId,
  projectName,
  packages,
  canEdit,
}: {
  projectId: string;
  projectName: string;
  packages?: SoftwarePackage[];
  canEdit: boolean;
}) {
  // Get the first package's git URL for GitHub linking.
  const gitUrl = packages?.[0]?.git_url;

  return (
    <div>
      <h3 className="text-md font-semibold mb-3">Security Findings</h3>
      <FindingsTable
        projectId={projectId}
        projectName={projectName}
        gitUrl={gitUrl}
        canEdit={canEdit}
      />
    </div>
  );
}

function ProviderKeysTab({ projectId }: { projectId: string }) {
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [provider, setProvider] = useState('anthropic');
  const [label, setLabel] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [endpointUrl, setEndpointUrl] = useState('');

  // NRP-issued LLM keys are also a project provider key, but obtained
  // via the NRP token-exchange flow. Show the installer here when NRP
  // is configured so users have one place to grab any key they need.
  const { linkStatus: nrpLinkStatus } = useNRPLinkSession();
  const showNRPInstaller = !!nrpLinkStatus?.oauth_configured;

  const { data: keys, isLoading } = useQuery({
    queryKey: ['provider-keys', projectId],
    queryFn: () => api.providerKeys.list(projectId),
  });

  const createMutation = useMutation({
    mutationFn: () =>
      api.providerKeys.create(projectId, {
        provider,
        label,
        api_key: apiKey,
        ...(provider === 'custom' || (provider === 'nrp' && endpointUrl)
          ? { endpoint_url: endpointUrl }
          : {}),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-keys', projectId] });
      setAdding(false);
      setLabel('');
      setApiKey('');
      setEndpointUrl('');
    },
  });

  const revokeMutation = useMutation({
    mutationFn: (keyId: string) => api.providerKeys.revoke(projectId, keyId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-keys', projectId] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (keyId: string) => api.providerKeys.delete(projectId, keyId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-keys', projectId] });
    },
  });

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h3 className="text-md font-semibold">LLM Providers</h3>
          <p className="text-xs text-gray-500">
            API keys are encrypted at rest. Only the last 4 characters are ever displayed.
          </p>
        </div>
        {!adding && (
          <button
            onClick={() => setAdding(true)}
            className="bg-brand-600 text-white px-3 py-1.5 rounded hover:bg-brand-700 text-sm"
          >
            Add Key
          </button>
        )}
      </div>

      {showNRPInstaller && (
        <div className="mb-4">
          <NRPLLMKeyInstaller projectId={projectId} />
        </div>
      )}

      {adding && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            createMutation.mutate();
          }}
          className="border rounded p-4 mb-4 space-y-3 bg-gray-50"
        >
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Provider
            </label>
            <select
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
              className="w-full border rounded px-3 py-2"
            >
              <option value="anthropic">Anthropic</option>
              <option value="nrp">NRP (ACCESS)</option>
              <option value="custom">Custom Endpoint</option>
            </select>
          </div>
          {(provider === 'custom' || provider === 'nrp') && (
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">
                Endpoint URL {provider === 'custom' && <span className="text-red-500">*</span>}
              </label>
              <input
                type="url"
                value={endpointUrl}
                onChange={(e) => setEndpointUrl(e.target.value)}
                required={provider === 'custom'}
                placeholder="https://api.example.com/v1"
                className="w-full border rounded px-3 py-2 font-mono text-sm"
              />
              {provider === 'nrp' && (
                <p className="text-xs text-gray-500 mt-1">
                  Optional. Leave empty to use the global NRP endpoint.
                </p>
              )}
            </div>
          )}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Label
            </label>
            <input
              type="text"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="e.g. Production key"
              className="w-full border rounded px-3 py-2"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              API Key
            </label>
            <input
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              required
              placeholder="sk-ant-..."
              className="w-full border rounded px-3 py-2 font-mono"
            />
          </div>
          {createMutation.isError && (
            <p className="text-red-600 text-sm">
              {createMutation.error?.message || 'An unexpected error occurred'}
            </p>
          )}
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={createMutation.isPending}
              className="bg-brand-600 text-white px-4 py-2 rounded hover:bg-brand-700 disabled:opacity-50 text-sm"
            >
              {createMutation.isPending ? 'Saving...' : 'Save Key'}
            </button>
            <button
              type="button"
              onClick={() => {
                setAdding(false);
                setApiKey('');
                setEndpointUrl('');
              }}
              className="border px-4 py-2 rounded text-sm"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {isLoading ? (
        <p className="text-sm text-gray-500">Loading...</p>
      ) : !keys?.length ? (
        <p className="text-sm text-gray-500">No provider keys configured.</p>
      ) : (
        <div className="border rounded divide-y">
          {keys.map((k) => (
            <div
              key={k.id}
              className={`flex items-center justify-between px-4 py-3 ${
                !k.is_active ? 'opacity-50' : ''
              }`}
            >
              <div>
                <div className="flex items-center gap-2">
                  <span className="text-xs font-semibold uppercase bg-gray-100 text-gray-700 px-2 py-0.5 rounded">
                    {k.provider}
                  </span>
                  <span className="font-medium">{k.label || 'Unnamed'}</span>
                  <code className="text-xs text-gray-500">{k.key_hint}</code>
                </div>
                <div className="text-xs text-gray-500 mt-1">
                  Added {new Date(k.created_at).toLocaleDateString()}
                  {k.revoked_at && (
                    <span className="text-red-500 ml-2">
                      Revoked {new Date(k.revoked_at).toLocaleDateString()}
                    </span>
                  )}
                </div>
              </div>
              {k.is_active && (
                <div className="flex gap-2">
                  <button
                    onClick={() => {
                      if (confirm('Revoke this key? It will no longer be usable.')) {
                        revokeMutation.mutate(k.id);
                      }
                    }}
                    className="text-yellow-600 hover:text-yellow-800 text-sm"
                  >
                    Revoke
                  </button>
                  <button
                    onClick={() => {
                      if (confirm('Permanently delete this key?')) {
                        deleteMutation.mutate(k.id);
                      }
                    }}
                    className="text-red-600 hover:text-red-800 text-sm"
                  >
                    Delete
                  </button>
                </div>
              )}
              {!k.is_active && (
                <button
                  onClick={() => {
                    if (confirm('Permanently delete this revoked key?')) {
                      deleteMutation.mutate(k.id);
                    }
                  }}
                  className="text-red-600 hover:text-red-800 text-sm"
                >
                  Delete
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
