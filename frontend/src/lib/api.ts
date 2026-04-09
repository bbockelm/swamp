const BASE = "/api/v1";

class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function fetchJSON<T>(url: string, opts?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...opts,
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...opts?.headers,
    },
  });
  if (res.status === 401) {
    // Redirect to login, preserving the current URL so the user returns
    // here after authenticating (e.g. /invite?token=... flow).
    const returnTo = window.location.pathname + window.location.search;
    window.location.href = `/login?return_to=${encodeURIComponent(returnTo)}`;
    throw new ApiError(401, "Unauthorized");
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new ApiError(res.status, body.error || res.statusText);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

// --- Types ---

export interface User {
  id: string;
  email: string;
  display_name: string;
  status: string;
  last_login: string | null;
  created_at: string;
  updated_at: string;
}

export interface UserRole {
  id: string;
  user_id: string;
  role: string;
  created_at: string;
}

export interface UserIdentity {
  id: string;
  user_id: string;
  issuer: string;
  subject: string;
  email: string;
  display_name: string;
  idp_name: string;
  created_at: string;
}

export interface Session {
  authenticated: boolean;
  user: User | null;
  roles: string[];
  aup_agreed: boolean;
  aup_version: string;
  aup_text: string;
}

export interface Group {
  id: string;
  name: string;
  description: string;
  owner_id: string;
  admin_group_id?: string | null;
  my_role?: string;
  created_at: string;
  updated_at: string;
}

export interface GroupMember {
  id: string;
  group_id: string;
  user_id: string;
  role: string;
  added_by: string;
  created_at: string;
  display_name?: string;
  email?: string;
}

export interface GroupInvite {
  id: string;
  group_id: string;
  email: string;
  role: string;
  token: string;
  used: boolean;
  expires_at: string;
  created_by: string;
  created_at: string;
}

export interface Project {
  id: string;
  name: string;
  description: string;
  owner_id: string;
  read_group_id: string | null;
  write_group_id: string | null;
  admin_group_id: string | null;
  uses_global_key: boolean;
  status: string;
  my_role?: string;
  created_at: string;
  updated_at: string;
}

export interface SoftwarePackage {
  id: string;
  project_id: string;
  name: string;
  git_url: string;
  git_branch: string;
  git_commit: string;
  analysis_prompt: string;
  created_at: string;
  updated_at: string;
}

export interface Analysis {
  id: string;
  project_id: string;
  project_name?: string;
  status: string;
  status_detail: string;
  error_message: string;
  custom_prompt: string;
  git_commit: string;
  triggered_by: string;
  triggered_by_name?: string;
  started_at: string;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface AnalysisResult {
  id: string;
  analysis_id: string;
  result_type: string;
  filename: string;
  s3_key: string;
  content_type: string;
  file_size: number;
  finding_count: number;
  severity_counts: Record<string, number>;
  created_at: string;
}

export interface APIKey {
  id: string;
  name: string;
  key_prefix: string;
  user_id: string;
  expires_at: string | null;
  last_used_at: string | null;
  created_at: string;
}

export interface ProjectProviderKey {
  id: string;
  project_id: string;
  provider: string;
  label: string;
  key_hint: string;
  endpoint_url?: string;
  api_schema: string;
  is_active: boolean;
  created_by: string;
  created_at: string;
  revoked_at: string | null;
}

export interface LLMProvider {
  id: string;
  label: string;
  api_schema: string;
  base_url: string;
  default_model: string;
  key_hint: string;
  enabled: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface AvailableProvider {
  id: string;
  source: "global" | "project" | "env";
  label: string;
  api_schema: string;
  base_url: string;
  default_model: string;
}

export interface ProjectAllowedProvider {
  id: string;
  project_id: string;
  provider_id: string;
  provider_source: "global" | "env";
  created_at: string;
  created_by: string;
}

export interface DiscoveredModel {
  id: string;
  display_name?: string;
}

export interface Backup {
  id: string;
  filename: string;
  s3_key: string;
  s3_bucket: string;
  size_bytes: number;
  status: string;
  status_detail: string;
  error_msg: string;
  initiated_by: string;
  encrypted: boolean;
  checksum: string;
  duration_secs: number;
  started_at: string | null;
  completed_at: string | null;
  created_at: string;
}

export interface UserInvite {
  id: string;
  token?: string;
  created_by: string;
  email: string;
  used: boolean;
  used_by: string | null;
  expires_at: string;
  created_at: string;
}

export interface OIDCConfig {
  oidc_issuer: string;
  oidc_client_id: string;
  secret_set: boolean;
  callback_url: string;
}

export interface Finding {
  id: string;
  project_id: string;
  analysis_id: string;
  result_id: string;
  rule_id: string;
  level: string;
  message: string;
  file_path: string;
  start_line: number;
  end_line: number;
  snippet: string;
  fingerprint: string;
  raw_json: Record<string, unknown>;
  created_at: string;
  latest_status: string;
  latest_note: string;
  annotation_by: string;
  git_url?: string;
  git_commit?: string;
}

export interface FindingAnnotation {
  id: string;
  finding_id: string;
  user_id: string;
  user_display_name: string;
  status: string;
  note: string;
  created_at: string;
  updated_at: string;
}

export interface FindingsResponse {
  findings: Finding[];
  total: number;
  limit: number;
  offset: number;
}

export interface BackupSettings {
  backup_frequency_hours: number;
  backup_bucket: string;
  backup_endpoint: string;
  backup_access_key: string;
  backup_secret_key: string;
  backup_use_ssl: boolean;
}

export interface AUPConfig {
  version: string;
  text: string;
  agreed: number;
  total_users: number;
  users: AUPUserStatus[];
}

export interface LogEntry {
  timestamp: string;
  level: string;
  message: string;
  fields?: Record<string, string>;
}

export interface AUPUserStatus {
  user_id: string;
  display_name: string;
  email: string;
  status: string;
  agreed_at: string | null;
}

export interface DashboardStats {
  project_count: number;
  group_count: number;
  analysis_counts: Record<string, number>;
  recent_analyses: Analysis[];
  total_findings: number;
  severity_counts: Record<string, number>;
}

export interface UserStats {
  group_count: number;
  project_count: number;
  package_count: number;
  analysis_count: number;
  finding_count: number;
  member_since: string;
}

// --- API client ---

export const api = {
  dashboard: {
    stats: (): Promise<DashboardStats> =>
      fetchJSON(`${BASE}/dashboard/stats`),
  },

  agent: {
    status: (): Promise<{
      ready: boolean;
      provider?: string;
      default_model?: string;
      models?: { id: string; name: string }[];
    }> => fetchJSON(`${BASE}/agent/status`),
  },

  users: {
    search: (q: string): Promise<User[]> =>
      fetchJSON(`${BASE}/users/search?q=${encodeURIComponent(q)}`),
  },

  auth: {
    me: (): Promise<Session> => fetchJSON(`${BASE}/auth/me`),
    logout: (): Promise<void> =>
      fetchJSON(`${BASE}/auth/logout`, { method: "POST" }),
    agreeAup: (aupVersion: string): Promise<void> =>
      fetchJSON(`${BASE}/auth/agree-aup`, {
        method: "POST",
        body: JSON.stringify({ aup_version: aupVersion }),
      }),
    updateProfile: (displayName: string): Promise<User> =>
      fetchJSON(`${BASE}/auth/profile`, {
        method: "PUT",
        body: JSON.stringify({ display_name: displayName }),
      }),
    myStats: (): Promise<UserStats> =>
      fetchJSON(`${BASE}/auth/my-stats`),
  },

  groups: {
    list: (): Promise<Group[]> => fetchJSON(`${BASE}/groups`),
    create: (data: Partial<Group>): Promise<Group> =>
      fetchJSON(`${BASE}/groups`, {
        method: "POST",
        body: JSON.stringify(data),
      }),
    get: (id: string): Promise<Group> => fetchJSON(`${BASE}/groups/${id}`),
    update: (id: string, data: Partial<Group>): Promise<Group> =>
      fetchJSON(`${BASE}/groups/${id}`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    delete: (id: string): Promise<void> =>
      fetchJSON(`${BASE}/groups/${id}`, { method: "DELETE" }),
    listMembers: (id: string): Promise<GroupMember[]> =>
      fetchJSON(`${BASE}/groups/${id}/members`),
    addMember: (
      id: string,
      data: { user_id: string; role: string },
    ): Promise<GroupMember> =>
      fetchJSON(`${BASE}/groups/${id}/members`, {
        method: "POST",
        body: JSON.stringify(data),
      }),
    removeMember: (groupId: string, userId: string): Promise<void> =>
      fetchJSON(`${BASE}/groups/${groupId}/members/${userId}`, {
        method: "DELETE",
      }),
    updateMemberRole: (groupId: string, userId: string, role: string): Promise<void> =>
      fetchJSON(`${BASE}/groups/${groupId}/members/${userId}`, {
        method: "PUT",
        body: JSON.stringify({ role }),
      }),
    listInvites: (id: string): Promise<GroupInvite[]> =>
      fetchJSON(`${BASE}/groups/${id}/invites`),
    createInvite: (
      id: string,
      data: { email?: string; role: string },
    ): Promise<GroupInvite> =>
      fetchJSON(`${BASE}/groups/${id}/invites`, {
        method: "POST",
        body: JSON.stringify(data),
      }),
    deleteInvite: (groupId: string, inviteId: string): Promise<void> =>
      fetchJSON(`${BASE}/groups/${groupId}/invites/${inviteId}`, {
        method: "DELETE",
      }),
    acceptInvite: (token: string): Promise<{ status: string; group_id: string }> =>
      fetchJSON(`${BASE}/invites/accept`, {
        method: "POST",
        body: JSON.stringify({ token }),
      }),
    inviteInfo: (token: string): Promise<{ group_name: string; role: string }> =>
      fetchJSON(`${BASE}/invites/info?token=${encodeURIComponent(token)}`),
  },

  projects: {
    list: (): Promise<Project[]> => fetchJSON(`${BASE}/projects`),
    create: (data: Partial<Project>): Promise<Project> =>
      fetchJSON(`${BASE}/projects`, {
        method: "POST",
        body: JSON.stringify(data),
      }),
    get: (id: string): Promise<Project> => fetchJSON(`${BASE}/projects/${id}`),
    update: (id: string, data: Partial<Project>): Promise<Project> =>
      fetchJSON(`${BASE}/projects/${id}`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    delete: (id: string): Promise<void> =>
      fetchJSON(`${BASE}/projects/${id}`, { method: "DELETE" }),
  },

  packages: {
    list: (projectId: string): Promise<SoftwarePackage[]> =>
      fetchJSON(`${BASE}/projects/${projectId}/packages`),
    create: (
      projectId: string,
      data: Partial<SoftwarePackage>,
    ): Promise<SoftwarePackage> =>
      fetchJSON(`${BASE}/projects/${projectId}/packages`, {
        method: "POST",
        body: JSON.stringify(data),
      }),
    get: (projectId: string, id: string): Promise<SoftwarePackage> =>
      fetchJSON(`${BASE}/projects/${projectId}/packages/${id}`),
    update: (
      projectId: string,
      id: string,
      data: Partial<SoftwarePackage>,
    ): Promise<SoftwarePackage> =>
      fetchJSON(`${BASE}/projects/${projectId}/packages/${id}`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    delete: (projectId: string, id: string): Promise<void> =>
      fetchJSON(`${BASE}/projects/${projectId}/packages/${id}`, {
        method: "DELETE",
      }),
  },

  analyses: {
    listAll: (): Promise<Analysis[]> => fetchJSON(`${BASE}/analyses`),
    list: (projectId: string): Promise<Analysis[]> =>
      fetchJSON(`${BASE}/projects/${projectId}/analyses`),
    create: (
      projectId: string,
      data: { package_ids: string[]; agent_model?: string; custom_prompt?: string; provider_id?: string; provider_source?: string },
    ): Promise<Analysis> =>
      fetchJSON(`${BASE}/projects/${projectId}/analyses`, {
        method: "POST",
        body: JSON.stringify(data),
      }),
    get: async (projectId: string, id: string): Promise<Analysis> => {
      const resp = await fetchJSON<{ analysis: Analysis }>(
        `${BASE}/projects/${projectId}/analyses/${id}`,
      );
      return resp.analysis;
    },
    cancel: (projectId: string, id: string): Promise<void> =>
      fetchJSON(`${BASE}/projects/${projectId}/analyses/${id}/cancel`, {
        method: "POST",
      }),
    checkAlive: (projectId: string, id: string): Promise<{ alive: boolean }> =>
      fetchJSON(`${BASE}/projects/${projectId}/analyses/${id}/alive`),
    resubmit: (projectId: string, id: string): Promise<Analysis> =>
      fetchJSON(`${BASE}/projects/${projectId}/analyses/${id}/resubmit`, {
        method: "POST",
      }),
    listResults: (
      projectId: string,
      analysisId: string,
    ): Promise<AnalysisResult[]> =>
      fetchJSON(`${BASE}/projects/${projectId}/analyses/${analysisId}/results`),
    getResult: (
      projectId: string,
      analysisId: string,
      resultId: string,
    ): Promise<AnalysisResult> =>
      fetchJSON(
        `${BASE}/projects/${projectId}/analyses/${analysisId}/results/${resultId}`,
      ),
    downloadResult: (
      projectId: string,
      analysisId: string,
      resultId: string,
    ): string =>
      `${BASE}/projects/${projectId}/analyses/${analysisId}/results/${resultId}/download`,
  },

  findings: {
    listAll: (
      params?: {
        level?: string;
        rule_id?: string;
        status?: string;
        file_path?: string;
        search?: string;
        limit?: number;
        offset?: number;
      },
    ): Promise<FindingsResponse> => {
      const qs = new URLSearchParams();
      if (params) {
        Object.entries(params).forEach(([k, v]) => {
          if (v !== undefined && v !== "") qs.set(k, String(v));
        });
      }
      const query = qs.toString();
      return fetchJSON(`${BASE}/findings${query ? "?" + query : ""}`);
    },
    list: (
      projectId: string,
      params?: {
        level?: string;
        rule_id?: string;
        status?: string;
        analysis_id?: string;
        file_path?: string;
        search?: string;
        limit?: number;
        offset?: number;
      },
    ): Promise<FindingsResponse> => {
      const qs = new URLSearchParams();
      if (params) {
        Object.entries(params).forEach(([k, v]) => {
          if (v !== undefined && v !== "") qs.set(k, String(v));
        });
      }
      const query = qs.toString();
      return fetchJSON(
        `${BASE}/projects/${projectId}/findings${query ? "?" + query : ""}`,
      );
    },
    get: (
      projectId: string,
      findingId: string,
    ): Promise<{ finding: Finding; annotations: FindingAnnotation[] }> =>
      fetchJSON(`${BASE}/projects/${projectId}/findings/${findingId}`),
    annotate: (
      projectId: string,
      findingId: string,
      data: { status: string; note: string },
    ): Promise<FindingAnnotation> =>
      fetchJSON(`${BASE}/projects/${projectId}/findings/${findingId}/annotate`, {
        method: "POST",
        body: JSON.stringify(data),
      }),
    listAnnotations: (
      projectId: string,
      findingId: string,
    ): Promise<FindingAnnotation[]> =>
      fetchJSON(
        `${BASE}/projects/${projectId}/findings/${findingId}/annotations`,
      ),
  },

  apiKeys: {
    list: (): Promise<APIKey[]> => fetchJSON(`${BASE}/api-keys`),
    create: (data: {
      name: string;
      expires_in?: string;
    }): Promise<APIKey & { key: string }> =>
      fetchJSON(`${BASE}/api-keys`, {
        method: "POST",
        body: JSON.stringify(data),
      }),
    revoke: (id: string): Promise<void> =>
      fetchJSON(`${BASE}/api-keys/${id}`, { method: "DELETE" }),
  },

  providerKeys: {
    list: (projectId: string): Promise<ProjectProviderKey[]> =>
      fetchJSON(`${BASE}/projects/${projectId}/provider-keys`),
    create: (
      projectId: string,
      data: { provider: string; label: string; api_key: string; endpoint_url?: string; api_schema?: string },
    ): Promise<ProjectProviderKey> =>
      fetchJSON(`${BASE}/projects/${projectId}/provider-keys`, {
        method: "POST",
        body: JSON.stringify(data),
      }),
    revoke: (projectId: string, keyId: string): Promise<void> =>
      fetchJSON(
        `${BASE}/projects/${projectId}/provider-keys/${keyId}/revoke`,
        { method: "POST" },
      ),
    delete: (projectId: string, keyId: string): Promise<void> =>
      fetchJSON(`${BASE}/projects/${projectId}/provider-keys/${keyId}`, {
        method: "DELETE",
      }),
    discoverModels: (projectId: string, keyId: string): Promise<DiscoveredModel[]> =>
      fetchJSON(`${BASE}/projects/${projectId}/provider-keys/${keyId}/models`),
  },

  availableProviders: (projectId: string): Promise<AvailableProvider[]> =>
    fetchJSON(`${BASE}/projects/${projectId}/available-providers`),

  allProviders: (projectId: string): Promise<AvailableProvider[]> =>
    fetchJSON(`${BASE}/projects/${projectId}/available-providers?include_all=true`),

  allowedProviders: {
    list: (projectId: string): Promise<ProjectAllowedProvider[]> =>
      fetchJSON(`${BASE}/projects/${projectId}/allowed-providers`),
    add: (projectId: string, providerId: string, providerSource: string): Promise<void> =>
      fetchJSON(`${BASE}/projects/${projectId}/allowed-providers`, {
        method: "POST",
        body: JSON.stringify({ provider_id: providerId, provider_source: providerSource }),
      }),
    remove: (projectId: string, providerId: string, providerSource: string): Promise<void> =>
      fetchJSON(`${BASE}/projects/${projectId}/allowed-providers`, {
        method: "DELETE",
        body: JSON.stringify({ provider_id: providerId, provider_source: providerSource }),
      }),
  },

  llmProviders: {
    list: (): Promise<LLMProvider[]> =>
      fetchJSON(`${BASE}/admin/llm-providers`),
    create: (data: {
      label: string;
      api_schema: string;
      base_url: string;
      default_model?: string;
      api_key: string;
      enabled?: boolean;
    }): Promise<LLMProvider> =>
      fetchJSON(`${BASE}/admin/llm-providers`, {
        method: "POST",
        body: JSON.stringify(data),
      }),
    update: (
      id: string,
      data: { label: string; api_schema: string; base_url: string; default_model?: string; api_key?: string; enabled?: boolean },
    ): Promise<LLMProvider> =>
      fetchJSON(`${BASE}/admin/llm-providers/${id}`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    delete: (id: string): Promise<void> =>
      fetchJSON(`${BASE}/admin/llm-providers/${id}`, { method: "DELETE" }),
    discoverModels: (id: string): Promise<DiscoveredModel[]> =>
      fetchJSON(`${BASE}/admin/llm-providers/${id}/models`),
    discoverEnvModels: (id: string): Promise<DiscoveredModel[]> =>
      fetchJSON(`${BASE}/admin/env-providers/${id}/models`),
  },

  admin: {
    listUsers: (): Promise<User[]> => fetchJSON(`${BASE}/admin/users`),
    createUser: (displayName: string, role: string): Promise<User> =>
      fetchJSON(`${BASE}/admin/users`, {
        method: "POST",
        body: JSON.stringify({ display_name: displayName, role }),
      }),
    getUser: (id: string): Promise<User> =>
      fetchJSON(`${BASE}/admin/users/${id}`),
    updateUser: (id: string, data: Partial<User>): Promise<User> =>
      fetchJSON(`${BASE}/admin/users/${id}`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    deleteUser: (id: string): Promise<void> =>
      fetchJSON(`${BASE}/admin/users/${id}`, { method: "DELETE" }),
    listValidRoles: (): Promise<string[]> =>
      fetchJSON(`${BASE}/admin/roles`),
    listUserRoles: (userId: string): Promise<UserRole[]> =>
      fetchJSON(`${BASE}/admin/users/${userId}/roles`),
    addRole: (userId: string, role: string): Promise<void> =>
      fetchJSON(`${BASE}/admin/users/${userId}/roles`, {
        method: "POST",
        body: JSON.stringify({ role }),
      }),
    removeRole: (userId: string, role: string): Promise<void> =>
      fetchJSON(`${BASE}/admin/users/${userId}/roles/${role}`, {
        method: "DELETE",
      }),
    listUserIdentities: (userId: string): Promise<UserIdentity[]> =>
      fetchJSON(`${BASE}/admin/users/${userId}/identities`),
    deleteUserIdentity: (
      userId: string,
      identityId: string,
    ): Promise<void> =>
      fetchJSON(`${BASE}/admin/users/${userId}/identities/${identityId}`, {
        method: "DELETE",
      }),
    listUserGroups: (userId: string): Promise<Group[]> =>
      fetchJSON(`${BASE}/admin/users/${userId}/groups`),
    listUserProjects: (userId: string): Promise<Project[]> =>
      fetchJSON(`${BASE}/admin/users/${userId}/projects`),
    createInvite: (
      userId: string,
    ): Promise<{ invite: UserInvite; invite_url: string }> =>
      fetchJSON(`${BASE}/admin/users/${userId}/invites`, {
        method: "POST",
        body: "{}",
      }),
    listInvites: (userId: string): Promise<UserInvite[]> =>
      fetchJSON(`${BASE}/admin/users/${userId}/invites`),
    deleteInvite: (userId: string, inviteId: string): Promise<void> =>
      fetchJSON(`${BASE}/admin/users/${userId}/invites/${inviteId}`, {
        method: "DELETE",
      }),
    getOIDCConfig: (): Promise<OIDCConfig> =>
      fetchJSON(`${BASE}/admin/oidc-config`),
    updateOIDCConfig: (data: {
      oidc_issuer?: string;
      oidc_client_id?: string;
      oidc_client_secret?: string;
    }): Promise<void> =>
      fetchJSON(`${BASE}/admin/oidc-config`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    listBackups: (): Promise<Backup[]> => fetchJSON(`${BASE}/admin/backups`),
    triggerBackup: (): Promise<Backup> =>
      fetchJSON(`${BASE}/admin/backups/trigger`, { method: "POST" }),
    deleteBackup: (id: string): Promise<void> =>
      fetchJSON(`${BASE}/admin/backups/${id}`, { method: "DELETE" }),
    deleteFailedBackups: (): Promise<{ status: string; count: number }> =>
      fetchJSON(`${BASE}/admin/backups/failed`, { method: "DELETE" }),
    getPerBackupKey: (id: string): Promise<{ key: string }> =>
      fetchJSON(`${BASE}/admin/backups/${id}/key`),
    getGeneralBackupKey: (): Promise<{ key: string }> =>
      fetchJSON(`${BASE}/admin/backups/general-key`),
    downloadBackupUrl: (id: string): string =>
      `${BASE}/admin/backups/${id}/download`,
    restoreBackup: (id: string): Promise<{ status: string }> =>
      fetchJSON(`${BASE}/admin/backups/${id}/restore`, { method: "POST" }),
    uploadRestore: async (
      file: File,
      encrypted = true,
      decryptKey?: string,
    ): Promise<{ status: string }> => {
      const form = new FormData();
      form.append("file", file);
      form.append("encrypted", String(encrypted));
      if (decryptKey) form.append("decrypt_key", decryptKey);
      const res = await fetch(`${BASE}/admin/backups/upload-restore`, {
        method: "POST",
        body: form,
        credentials: "include",
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new ApiError(
          res.status,
          body.error || `Upload failed: ${res.status}`,
        );
      }
      return res.json();
    },
    getBackupSettings: (): Promise<BackupSettings> =>
      fetchJSON(`${BASE}/admin/backups/settings`),
    updateBackupSettings: (
      data: Partial<BackupSettings>,
    ): Promise<BackupSettings> =>
      fetchJSON(`${BASE}/admin/backups/settings`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    getAUPConfig: (): Promise<AUPConfig> =>
      fetchJSON(`${BASE}/admin/aup`),
    updateAUPConfig: (data: { version?: string; text?: string }): Promise<void> =>
      fetchJSON(`${BASE}/admin/aup`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    getExecutorConfig: (): Promise<Record<string, string>> =>
      fetchJSON(`${BASE}/admin/executor-config`),
    updateExecutorConfig: (
      data: Record<string, string>,
    ): Promise<void> =>
      fetchJSON(`${BASE}/admin/executor-config`, {
        method: "PUT",
        body: JSON.stringify(data),
      }),
    getRecentLogs: (): Promise<LogEntry[]> =>
      fetchJSON(`${BASE}/admin/logs`),
  },
};
