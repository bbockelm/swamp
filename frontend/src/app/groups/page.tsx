"use client";

import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { api, type Group } from "@/lib/api";
import { GroupManager } from "@/components/GroupManager";

export default function GroupsPage() {
  const queryClient = useQueryClient();
  const { data: groups, isLoading } = useQuery({
    queryKey: ["groups"],
    queryFn: api.groups.list,
  });

  const [showCreateForm, setShowCreateForm] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const createGroup = useMutation({
    mutationFn: () => api.groups.create({ name, description }),
    onSuccess: (group) => {
      queryClient.invalidateQueries({ queryKey: ["groups"] });
      setName("");
      setDescription("");
      setShowCreateForm(false);
      setExpandedId(group.id);
    },
  });

  if (isLoading) return <p>Loading...</p>;

  return (
    <div>
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-2xl font-bold">Groups</h1>
        <button
          onClick={() => setShowCreateForm(!showCreateForm)}
          className={`px-4 py-2 rounded text-sm font-medium transition-colors ${
            showCreateForm
              ? "bg-gray-200 text-gray-700 hover:bg-gray-300"
              : "bg-blue-600 text-white hover:bg-blue-700"
          }`}
        >
          {showCreateForm ? "Cancel" : "+ New Group"}
        </button>
      </div>

      {/* Inline create form */}
      {showCreateForm && (
        <div className="mb-6 p-4 bg-gray-50 border rounded-lg">
          <form
            onSubmit={(e) => {
              e.preventDefault();
              createGroup.mutate();
            }}
            className="space-y-3"
          >
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
                  Group Name
                </label>
                <input
                  type="text"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="My Team"
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
            <div className="flex gap-2">
              <button
                type="submit"
                disabled={createGroup.isPending || !name.trim()}
                className="px-4 py-2 bg-green-600 text-white rounded text-sm font-medium hover:bg-green-700 disabled:opacity-50"
              >
                {createGroup.isPending ? "Creating..." : "Create"}
              </button>
            </div>
            {createGroup.isError && (
              <p className="text-sm text-red-600">
                Error: {createGroup.error?.message || 'An unexpected error occurred'}
              </p>
            )}
          </form>
        </div>
      )}

      {!groups?.length ? (
        <p className="text-gray-500">No groups yet.</p>
      ) : (
        <div className="space-y-2">
          {groups.map((g) => (
            <GroupCard
              key={g.id}
              group={g}
              expanded={expandedId === g.id}
              onToggle={() => setExpandedId(expandedId === g.id ? null : g.id)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ─── group card ─────────────────────────────────────────────

type GroupTab = "members" | "settings";

function GroupCard({
  group,
  expanded,
  onToggle,
}: {
  group: Group;
  expanded: boolean;
  onToggle: () => void;
}) {
  const [tab, setTab] = useState<GroupTab>("members");

  const tabs: { key: GroupTab; label: string }[] = [
    { key: "members", label: "Members & Invites" },
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
          <div className="font-medium text-gray-900">{group.name}</div>
          {group.description && (
            <div className="text-sm text-gray-500 truncate">
              {group.description}
            </div>
          )}
        </div>

        <span className="text-xs text-gray-400 flex-shrink-0">
          {new Date(group.created_at).toLocaleDateString()}
        </span>

        <Link
          href={`/groups/${group.id}`}
          onClick={(e) => e.stopPropagation()}
          className="p-1 text-gray-400 hover:text-blue-600 flex-shrink-0"
          title="Open group page"
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
            {tab === "members" && <GroupManager groupId={group.id} />}
            {tab === "settings" && <GroupSettings group={group} />}
          </div>
        </div>
      )}
    </div>
  );
}

// ─── group settings ─────────────────────────────────────────

function GroupSettings({ group }: { group: Group }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(group.name);
  const [description, setDescription] = useState(group.description);
  const [adminGroupId, setAdminGroupId] = useState<string | null>(group.admin_group_id ?? null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const { data: allGroups } = useQuery({
    queryKey: ["groups"],
    queryFn: api.groups.list,
  });

  const updateMutation = useMutation({
    mutationFn: () => api.groups.update(group.id, { name, description, admin_group_id: adminGroupId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["groups"] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.groups.delete(group.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["groups"] });
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

        <div>
          <label className="block text-xs font-medium text-gray-500 uppercase tracking-wide mb-1">
            Administrator Group
          </label>
          <select
            value={adminGroupId ?? ""}
            onChange={(e) => setAdminGroupId(e.target.value || null)}
            className="w-full border rounded px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            <option value="">None</option>
            {allGroups
              ?.filter((g) => g.id !== group.id)
              .map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
          </select>
          <p className="text-xs text-gray-500 mt-1">
            Members of the selected group will automatically have admin access.
          </p>
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
          Deleting the group removes all members and invites.
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
            Delete Group
          </button>
        )}
      </div>
    </div>
  );
}
