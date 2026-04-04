"use client";

import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, User } from "@/lib/api";

const ALL_ROLES = ["admin", "analyst", "viewer"];

function UserCard({ user }: { user: User }) {
  const [expanded, setExpanded] = useState(false);
  const [editingName, setEditingName] = useState(false);
  const [displayName, setDisplayName] = useState(user.display_name);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const queryClient = useQueryClient();

  const { data: roles } = useQuery({
    queryKey: ["admin", "user-roles", user.id],
    queryFn: () => api.admin.listUserRoles(user.id),
    enabled: expanded,
  });

  const { data: identities } = useQuery({
    queryKey: ["admin", "user-identities", user.id],
    queryFn: () => api.admin.listUserIdentities(user.id),
    enabled: expanded,
  });

  const updateUser = useMutation({
    mutationFn: (data: Partial<User>) => api.admin.updateUser(user.id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "users"] });
      setEditingName(false);
    },
  });

  const toggleStatus = useMutation({
    mutationFn: () =>
      api.admin.updateUser(user.id, {
        status: user.status === "active" ? "disabled" : "active",
      }),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ["admin", "users"] }),
  });

  const deleteUser = useMutation({
    mutationFn: () => api.admin.deleteUser(user.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "users"] });
      setConfirmDelete(false);
    },
  });

  const addRole = useMutation({
    mutationFn: (role: string) => api.admin.addRole(user.id, role),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["admin", "user-roles", user.id],
      }),
  });

  const removeRole = useMutation({
    mutationFn: (role: string) => api.admin.removeRole(user.id, role),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: ["admin", "user-roles", user.id],
      }),
  });

  const initials = (user.display_name || user.email || "?")
    .split(/[\s@]+/)
    .map((w) => w[0]?.toUpperCase())
    .slice(0, 2)
    .join("");

  const roleNames = roles?.map((r) => r.role) ?? [];
  const availableRoles = ALL_ROLES.filter((r) => !roleNames.includes(r));

  return (
    <div className="border rounded-lg bg-white shadow-sm">
      {/* Summary row */}
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center gap-4 px-4 py-3 text-left hover:bg-gray-50 transition-colors"
      >
        {/* Avatar */}
        <div className="flex-shrink-0 w-10 h-10 rounded-full bg-blue-600 text-white flex items-center justify-center text-sm font-semibold">
          {initials}
        </div>

        {/* Name + Email */}
        <div className="flex-1 min-w-0">
          <div className="font-medium text-gray-900 truncate">
            {user.display_name || "(no name)"}
          </div>
          <div className="text-sm text-gray-500 truncate">{user.email}</div>
        </div>

        {/* Status badge */}
        <span
          className={`text-xs px-2 py-1 rounded-full font-medium ${
            user.status === "active"
              ? "bg-green-100 text-green-800"
              : "bg-red-100 text-red-800"
          }`}
        >
          {user.status}
        </span>

        {/* Last login */}
        <div className="hidden sm:block text-sm text-gray-500 w-32 text-right">
          {user.last_login ? timeAgo(user.last_login) : "Never"}
        </div>

        {/* Expand arrow */}
        <svg
          className={`w-5 h-5 text-gray-400 transition-transform ${expanded ? "rotate-180" : ""}`}
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
        <div className="border-t px-4 py-4 space-y-4 bg-gray-50">
          {/* Display Name */}
          <div>
            <label className="text-xs font-medium text-gray-500 uppercase tracking-wide">
              Display Name
            </label>
            {editingName ? (
              <div className="flex items-center gap-2 mt-1">
                <input
                  type="text"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  className="border rounded px-2 py-1 text-sm flex-1"
                  autoFocus
                />
                <button
                  onClick={() =>
                    updateUser.mutate({ display_name: displayName })
                  }
                  className="text-sm text-blue-600 hover:text-blue-800 font-medium"
                >
                  Save
                </button>
                <button
                  onClick={() => {
                    setDisplayName(user.display_name);
                    setEditingName(false);
                  }}
                  className="text-sm text-gray-500 hover:text-gray-700"
                >
                  Cancel
                </button>
              </div>
            ) : (
              <div className="flex items-center gap-2 mt-1">
                <span className="text-sm">
                  {user.display_name || "(not set)"}
                </span>
                <button
                  onClick={() => setEditingName(true)}
                  className="text-xs text-blue-600 hover:text-blue-800"
                >
                  Edit
                </button>
              </div>
            )}
          </div>

          {/* Roles */}
          <div>
            <label className="text-xs font-medium text-gray-500 uppercase tracking-wide">
              Roles
            </label>
            <div className="flex flex-wrap items-center gap-2 mt-1">
              {roleNames.length === 0 && (
                <span className="text-sm text-gray-400 italic">
                  No roles assigned
                </span>
              )}
              {roleNames.map((role) => (
                <span
                  key={role}
                  className="inline-flex items-center gap-1 bg-blue-100 text-blue-800 text-xs font-medium px-2.5 py-1 rounded-full"
                >
                  {role}
                  <button
                    onClick={() => removeRole.mutate(role)}
                    className="hover:text-blue-600"
                    title={`Remove ${role} role`}
                  >
                    ×
                  </button>
                </span>
              ))}
              {availableRoles.length > 0 && (
                <select
                  onChange={(e) => {
                    if (e.target.value) {
                      addRole.mutate(e.target.value);
                      e.target.value = "";
                    }
                  }}
                  className="text-xs border rounded px-2 py-1 text-gray-600"
                  defaultValue=""
                >
                  <option value="" disabled>
                    + Add role
                  </option>
                  {availableRoles.map((r) => (
                    <option key={r} value={r}>
                      {r}
                    </option>
                  ))}
                </select>
              )}
            </div>
          </div>

          {/* Identities */}
          <div>
            <label className="text-xs font-medium text-gray-500 uppercase tracking-wide">
              Linked Identities
            </label>
            <div className="mt-1 space-y-1">
              {!identities?.length ? (
                <span className="text-sm text-gray-400 italic">
                  No identities
                </span>
              ) : (
                identities.map((id) => (
                  <div
                    key={id.id}
                    className="text-sm bg-white border rounded px-3 py-2 flex items-center gap-3"
                  >
                    <div className="flex-1 min-w-0">
                      <div className="font-medium truncate">
                        {id.idp_name || id.issuer}
                      </div>
                      <div className="text-xs text-gray-500 truncate">
                        {id.email || id.subject}
                      </div>
                    </div>
                    <div className="text-xs text-gray-400">
                      {new Date(id.created_at).toLocaleDateString()}
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>

          {/* Invite Links */}
          <InviteSection userId={user.id} />

          {/* Timestamps */}
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div>
              <span className="text-gray-500">Created:</span>{" "}
              {new Date(user.created_at).toLocaleString()}
            </div>
            <div>
              <span className="text-gray-500">Last login:</span>{" "}
              {user.last_login
                ? new Date(user.last_login).toLocaleString()
                : "Never"}
            </div>
          </div>

          {/* Actions */}
          <div className="flex items-center gap-3 pt-2 border-t">
            <button
              onClick={() => toggleStatus.mutate()}
              className={`text-sm font-medium px-3 py-1.5 rounded ${
                user.status === "active"
                  ? "bg-yellow-100 text-yellow-800 hover:bg-yellow-200"
                  : "bg-green-100 text-green-800 hover:bg-green-200"
              }`}
            >
              {user.status === "active" ? "Disable" : "Enable"}
            </button>

            {confirmDelete ? (
              <div className="flex items-center gap-2">
                <span className="text-sm text-red-600">Are you sure?</span>
                <button
                  onClick={() => deleteUser.mutate()}
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
                Delete
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function timeAgo(dateStr: string): string {
  const seconds = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(dateStr).toLocaleDateString();
}

export default function AdminUsersPage() {
  const queryClient = useQueryClient();
  const { data: users, isLoading } = useQuery({
    queryKey: ["admin", "users"],
    queryFn: api.admin.listUsers,
  });

  const [search, setSearch] = useState("");
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [newDisplayName, setNewDisplayName] = useState("");
  const [newRole, setNewRole] = useState("viewer");

  const createUser = useMutation({
    mutationFn: () => api.admin.createUser(newDisplayName, newRole),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "users"] });
      setNewDisplayName("");
      setNewRole("viewer");
      setShowCreateForm(false);
    },
  });

  const filtered = users?.filter(
    (u) =>
      u.display_name?.toLowerCase().includes(search.toLowerCase()) ||
      u.email?.toLowerCase().includes(search.toLowerCase()),
  );

  if (isLoading) {
    return (
      <div className="animate-pulse space-y-3">
        {[...Array(3)].map((_, i) => (
          <div key={i} className="h-16 bg-gray-200 rounded-lg" />
        ))}
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">Users</h1>
        <div className="flex items-center gap-3">
          <span className="text-sm text-gray-500">
            {users?.length ?? 0} users
          </span>
          <button
            onClick={() => setShowCreateForm(!showCreateForm)}
            className={`px-4 py-2 rounded text-sm font-medium transition-colors ${
              showCreateForm
                ? "bg-gray-200 text-gray-700 hover:bg-gray-300"
                : "bg-blue-600 text-white hover:bg-blue-700"
            }`}
          >
            {showCreateForm ? "Cancel" : "+ New User"}
          </button>
        </div>
      </div>

      {/* Inline create form */}
      {showCreateForm && (
        <div className="mb-6 p-4 bg-gray-50 border rounded-lg">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              createUser.mutate();
            }}
            className="flex flex-col sm:flex-row items-start sm:items-end gap-3"
          >
            <div className="flex-1 w-full sm:w-auto">
              <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
                Display Name
              </label>
              <input
                type="text"
                value={newDisplayName}
                onChange={(e) => setNewDisplayName(e.target.value)}
                placeholder="e.g. Jane Doe"
                required
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                autoFocus
              />
            </div>
            <div className="w-full sm:w-48">
              <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
                Role
              </label>
              <select
                value={newRole}
                onChange={(e) => setNewRole(e.target.value)}
                className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <option value="admin">Admin</option>
                <option value="project_creator">Project Creator</option>
                <option value="user">User</option>
              </select>
            </div>
            <button
              type="submit"
              disabled={createUser.isPending || !newDisplayName.trim()}
              className="px-4 py-2 bg-green-600 text-white rounded text-sm font-medium hover:bg-green-700 disabled:opacity-50"
            >
              {createUser.isPending ? "Creating..." : "Create"}
            </button>
          </form>
          {createUser.isError && (
            <p className="mt-2 text-sm text-red-600">
              Error: {(createUser.error as Error).message}
            </p>
          )}
        </div>
      )}

      {/* Search */}
      <div className="mb-4">
        <input
          type="text"
          placeholder="Search by name or email..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="w-full sm:w-80 border rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>

      {!filtered?.length ? (
        <p className="text-gray-500">
          {search ? "No users match your search." : "No users."}
        </p>
      ) : (
        <div className="space-y-2">
          {filtered.map((u) => (
            <UserCard key={u.id} user={u} />
          ))}
        </div>
      )}
    </div>
  );
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={() => {
        navigator.clipboard.writeText(text);
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
      }}
      className={`px-2 py-1 border rounded text-xs transition-colors ${
        copied
          ? "bg-green-100 border-green-300 text-green-700"
          : "bg-white hover:bg-gray-50"
      }`}
    >
      {copied ? "Copied" : "Copy"}
    </button>
  );
}

function InviteSection({ userId }: { userId: string }) {
  const queryClient = useQueryClient();
  const [generatedUrl, setGeneratedUrl] = useState("");

  const { data: invites } = useQuery({
    queryKey: ["admin", "invites", userId],
    queryFn: () => api.admin.listInvites(userId),
  });

  const createInviteMut = useMutation({
    mutationFn: () => api.admin.createInvite(userId),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["admin", "invites", userId] });
      setGeneratedUrl(data.invite_url);
    },
  });

  const deleteInviteMut = useMutation({
    mutationFn: (inviteId: string) => api.admin.deleteInvite(userId, inviteId),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ["admin", "invites", userId] }),
  });

  return (
    <div>
      <label className="text-xs font-medium text-gray-500 uppercase tracking-wide">
        Invite Links
      </label>

      {invites && invites.length > 0 && (
        <div className="mt-1 space-y-1 mb-2">
          {invites.map((inv) => (
            <div
              key={inv.id}
              className="flex items-center justify-between text-xs bg-white border rounded px-3 py-2"
            >
              <div>
                <span
                  className={
                    inv.used ? "text-gray-400 line-through" : "text-gray-700"
                  }
                >
                  Invite link
                </span>
                <span className="text-gray-400 ml-2">
                  expires {new Date(inv.expires_at).toLocaleDateString()}
                </span>
                {inv.used && <span className="ml-2 text-green-600">used</span>}
              </div>
              <button
                onClick={() => deleteInviteMut.mutate(inv.id)}
                className="text-red-400 hover:text-red-600"
              >
                ×
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="flex gap-2 items-center mt-1">
        <button
          onClick={() => createInviteMut.mutate()}
          disabled={createInviteMut.isPending}
          className="text-xs px-3 py-1 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
        >
          Generate Invite
        </button>
      </div>

      {generatedUrl && (
        <div className="mt-2 p-2 bg-green-50 border border-green-200 rounded text-xs">
          <div className="font-medium text-green-800 mb-1">
            Invite link generated:
          </div>
          <div className="flex items-center gap-2">
            <input
              type="text"
              readOnly
              value={generatedUrl}
              className="flex-1 bg-white border rounded px-2 py-1 text-xs"
            />
            <CopyButton text={generatedUrl} />
          </div>
        </div>
      )}
    </div>
  );
}
