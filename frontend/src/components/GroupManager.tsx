"use client";

import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type User } from "@/lib/api";
import { useState, useRef, useEffect } from "react";

export function GroupManager({ groupId, isAdmin }: { groupId: string; isAdmin: boolean }) {
  return (
    <div className="space-y-8">
      <MembersSection groupId={groupId} isAdmin={isAdmin} />
      <InvitesSection groupId={groupId} isAdmin={isAdmin} />
    </div>
  );
}

function UserSearch({ onSelect }: { onSelect: (user: User) => void }) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<User[]>([]);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (
        containerRef.current &&
        !containerRef.current.contains(e.target as Node)
      ) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  const doSearch = (q: string) => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    if (q.length < 2) {
      setResults([]);
      setOpen(false);
      return;
    }
    setLoading(true);
    debounceRef.current = setTimeout(async () => {
      try {
        const users = await api.users.search(q);
        setResults(users);
        setOpen(users.length > 0);
      } catch {
        setResults([]);
      } finally {
        setLoading(false);
      }
    }, 250);
  };

  return (
    <div ref={containerRef} className="relative flex-1">
      <input
        type="text"
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          doSearch(e.target.value);
        }}
        onFocus={() => {
          if (results.length > 0) setOpen(true);
        }}
        placeholder="Search by name or email..."
        className="w-full border rounded px-3 py-1.5 text-sm"
      />
      {loading && (
        <div className="absolute right-2 top-2 text-xs text-gray-400">...</div>
      )}
      {open && results.length > 0 && (
        <div className="absolute z-10 mt-1 w-full bg-white border rounded shadow-lg max-h-48 overflow-y-auto">
          {results.map((u) => (
            <button
              key={u.id}
              type="button"
              onClick={() => {
                onSelect(u);
                setQuery("");
                setResults([]);
                setOpen(false);
              }}
              className="w-full text-left px-3 py-2 hover:bg-blue-50 text-sm flex flex-col"
            >
              <span className="font-medium">
                {u.display_name || "(no name)"}
              </span>
              {u.email && (
                <span className="text-xs text-gray-500">{u.email}</span>
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function MembersSection({ groupId, isAdmin }: { groupId: string; isAdmin: boolean }) {
  const queryClient = useQueryClient();
  const [selectedUser, setSelectedUser] = useState<User | null>(null);
  const [addRole, setAddRole] = useState("member");
  const [confirmRemove, setConfirmRemove] = useState<string | null>(null);

  const { data: members, isLoading } = useQuery({
    queryKey: ["group-members", groupId],
    queryFn: () => api.groups.listMembers(groupId),
  });

  const addMutation = useMutation({
    mutationFn: () =>
      api.groups.addMember(groupId, {
        user_id: selectedUser!.id,
        role: addRole,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["group-members", groupId],
      });
      setSelectedUser(null);
    },
  });

  const removeMutation = useMutation({
    mutationFn: (userId: string) => api.groups.removeMember(groupId, userId),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["group-members", groupId],
      });
      setConfirmRemove(null);
    },
  });

  const roleMutation = useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: string }) =>
      api.groups.updateMemberRole(groupId, userId, role),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["group-members", groupId],
      });
    },
  });

  return (
    <div>
      <h2 className="text-lg font-semibold mb-4">Members</h2>

      {/* Add member form — admin only */}
      {isAdmin && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (selectedUser) addMutation.mutate();
          }}
          className="flex gap-2 mb-4 items-start"
        >
          {selectedUser ? (
            <div className="flex-1 flex items-center gap-2 border rounded px-3 py-1.5 bg-blue-50">
              <div className="flex-1 text-sm">
                <span className="font-medium">
                  {selectedUser.display_name || "(no name)"}
                </span>
                {selectedUser.email && (
                  <span className="text-gray-500 ml-1 text-xs">
                    {selectedUser.email}
                  </span>
                )}
              </div>
              <button
                type="button"
                onClick={() => setSelectedUser(null)}
                className="text-gray-400 hover:text-gray-600 text-sm"
              >
                &times;
              </button>
            </div>
          ) : (
            <UserSearch onSelect={setSelectedUser} />
          )}
          <select
            value={addRole}
            onChange={(e) => setAddRole(e.target.value)}
            className="border rounded px-3 py-1.5 text-sm"
          >
            <option value="member">Member</option>
            <option value="admin">Admin</option>
          </select>
          <button
            type="submit"
            disabled={addMutation.isPending || !selectedUser}
            className="bg-brand-600 text-white px-3 py-1.5 text-sm rounded hover:bg-brand-700 disabled:opacity-50"
          >
            Add
          </button>
        </form>
      )}

      {isAdmin && addMutation.isError && (
        <p className="text-sm text-red-600 mb-3">
          Error: {addMutation.error?.message || 'An unexpected error occurred'}
        </p>
      )}

      {isLoading ? (
        <p className="text-sm text-gray-500">Loading...</p>
      ) : !members?.length ? (
        <p className="text-sm text-gray-500">No members yet.</p>
      ) : (
        <div className="border rounded divide-y">
          {members.map((m) => (
            <div
              key={m.id}
              className="flex justify-between items-center px-4 py-3"
            >
              <div>
                <span className="text-sm font-medium">
                  {m.display_name || m.user_id.slice(0, 8)}
                </span>
                {m.email && (
                  <span className="text-xs text-gray-500 ml-2">{m.email}</span>
                )}
                <span
                  className={`ml-2 inline-block px-2 py-0.5 rounded text-xs font-medium ${
                    m.role === "admin"
                      ? "bg-purple-100 text-purple-800"
                      : "bg-gray-100 text-gray-800"
                  }`}
                >
                  {m.role}
                </span>
              </div>
              {isAdmin && (
                <div className="flex items-center gap-3">
                  <button
                    onClick={() =>
                      roleMutation.mutate({
                        userId: m.user_id,
                        role: m.role === "admin" ? "member" : "admin",
                      })
                    }
                    disabled={roleMutation.isPending}
                    className="text-xs text-brand-600 hover:text-brand-800"
                  >
                    {m.role === "admin" ? "Demote" : "Promote"}
                  </button>
                  {confirmRemove === m.user_id ? (
                    <div className="flex items-center gap-2">
                      <span className="text-xs text-red-600">Remove?</span>
                      <button
                        onClick={() => removeMutation.mutate(m.user_id)}
                        className="text-xs font-medium text-white bg-red-600 px-2 py-0.5 rounded hover:bg-red-700"
                      >
                        Yes
                      </button>
                      <button
                        onClick={() => setConfirmRemove(null)}
                        className="text-xs text-gray-500 hover:text-gray-700"
                      >
                        No
                      </button>
                    </div>
                  ) : (
                    <button
                      onClick={() => setConfirmRemove(m.user_id)}
                      className="text-xs text-red-500 hover:text-red-700"
                    >
                      Remove
                    </button>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function InvitesSection({ groupId, isAdmin }: { groupId: string; isAdmin: boolean }) {
  const queryClient = useQueryClient();
  const [role, setRole] = useState("member");
  const [newInviteLink, setNewInviteLink] = useState("");
  const [copied, setCopied] = useState(false);
  const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null);

  const { data: invites, isLoading } = useQuery({
    queryKey: ["group-invites", groupId],
    queryFn: () => api.groups.listInvites(groupId),
    enabled: isAdmin,
  });

  const createMutation = useMutation({
    mutationFn: () => api.groups.createInvite(groupId, { role }),
    onSuccess: (invite) => {
      queryClient.invalidateQueries({
        queryKey: ["group-invites", groupId],
      });
      if (invite.token) {
        const link = `${window.location.origin}/invite?token=${encodeURIComponent(invite.token)}`;
        setNewInviteLink(link);
        setCopied(false);
      }
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (inviteId: string) =>
      api.groups.deleteInvite(groupId, inviteId),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: ["group-invites", groupId],
      });
    },
  });

  const activeInvites = invites?.filter(
    (inv) => !inv.used && new Date(inv.expires_at) > new Date()
  ) ?? [];
  const pastInvites = invites?.filter(
    (inv) => inv.used || new Date(inv.expires_at) <= new Date()
  ) ?? [];

  if (!isAdmin) return null;

  return (
    <div>
      <h2 className="text-lg font-semibold mb-4">Invite Links</h2>

      <div className="flex gap-2 mb-4 items-center">
        <select
          value={role}
          onChange={(e) => setRole(e.target.value)}
          className="border rounded px-3 py-1.5 text-sm"
        >
          <option value="member">Member</option>
          <option value="admin">Admin</option>
        </select>
        <button
          onClick={() => createMutation.mutate()}
          disabled={createMutation.isPending}
          className="bg-brand-600 text-white px-3 py-1.5 text-sm rounded hover:bg-brand-700 disabled:opacity-50"
        >
          {createMutation.isPending ? "Creating..." : "Create Invite Link"}
        </button>
      </div>

      {createMutation.isError && (
        <p className="text-sm text-red-600 mb-3">
          Error: {createMutation.error?.message || 'An unexpected error occurred'}
        </p>
      )}

      {newInviteLink && (
        <div className="bg-green-50 border border-green-200 p-3 rounded mb-4 text-sm">
          <p className="font-medium text-green-800 mb-1">Invite link created!</p>
          <p className="text-xs text-green-700 mb-2">Share this link with the person you want to invite. It expires in 7 days and can only be used once.</p>
          <div className="flex gap-2 items-center">
            <input
              type="text"
              readOnly
              value={newInviteLink}
              className="flex-1 font-mono text-xs bg-white border border-green-300 rounded px-2 py-1.5"
              onFocus={(e) => e.target.select()}
            />
            <button
              onClick={() => {
                navigator.clipboard.writeText(newInviteLink);
                setCopied(true);
                setTimeout(() => setCopied(false), 2000);
              }}
              className="px-3 py-1.5 text-xs rounded border border-green-300 bg-white text-green-700 hover:bg-green-100"
            >
              {copied ? "Copied!" : "Copy"}
            </button>
          </div>
          <button
            onClick={() => setNewInviteLink("")}
            className="mt-2 text-xs text-green-600 hover:underline"
          >
            Dismiss
          </button>
        </div>
      )}

      {isLoading ? (
        <p className="text-sm text-gray-500">Loading...</p>
      ) : !invites?.length ? (
        <p className="text-sm text-gray-500">No invites yet.</p>
      ) : (
        <div className="space-y-4">
          {activeInvites.length > 0 && (
            <div>
              <h3 className="text-sm font-medium text-gray-600 mb-2">Active</h3>
              <div className="border rounded divide-y">
                {activeInvites.map((inv) => (
                  <div
                    key={inv.id}
                    className="flex justify-between items-center px-4 py-3"
                  >
                    <div className="flex items-center gap-2">
                      <span
                        className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${
                          inv.role === "admin"
                            ? "bg-purple-100 text-purple-800"
                            : "bg-gray-100 text-gray-800"
                        }`}
                      >
                        {inv.role}
                      </span>
                      {inv.email && (
                        <span className="text-sm text-gray-600">{inv.email}</span>
                      )}
                      <span className="text-xs text-gray-400">
                        expires {new Date(inv.expires_at).toLocaleDateString()}
                      </span>
                    </div>
                    {confirmRevoke === inv.id ? (
                      <div className="flex items-center gap-2">
                        <span className="text-xs text-red-600">Revoke?</span>
                        <button
                          onClick={() => {
                            deleteMutation.mutate(inv.id);
                            setConfirmRevoke(null);
                          }}
                          className="text-xs font-medium text-white bg-red-600 px-2 py-1 rounded hover:bg-red-700"
                        >
                          Yes
                        </button>
                        <button
                          onClick={() => setConfirmRevoke(null)}
                          className="text-xs text-gray-500 hover:text-gray-700"
                        >
                          No
                        </button>
                      </div>
                    ) : (
                      <button
                        onClick={() => setConfirmRevoke(inv.id)}
                        className="text-red-500 text-xs hover:text-red-700"
                      >
                        Revoke
                      </button>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}

          {pastInvites.length > 0 && (
            <div>
              <h3 className="text-sm font-medium text-gray-400 mb-2">Past</h3>
              <div className="border rounded divide-y opacity-60">
                {pastInvites.map((inv) => (
                  <div
                    key={inv.id}
                    className="flex justify-between items-center px-4 py-3"
                  >
                    <div className="flex items-center gap-2">
                      <span
                        className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${
                          inv.role === "admin"
                            ? "bg-purple-100 text-purple-800"
                            : "bg-gray-100 text-gray-800"
                        }`}
                      >
                        {inv.role}
                      </span>
                      {inv.email && (
                        <span className="text-sm text-gray-600">{inv.email}</span>
                      )}
                      {inv.used ? (
                        <span className="text-xs text-green-600">Used</span>
                      ) : (
                        <span className="text-xs text-red-500">Expired</span>
                      )}
                    </div>
                    <span className="text-xs text-gray-400">
                      {new Date(inv.created_at).toLocaleDateString()}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
