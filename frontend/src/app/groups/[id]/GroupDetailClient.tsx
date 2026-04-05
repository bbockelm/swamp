'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { GroupManager } from '@/components/GroupManager';
import { useResolvedParams } from '@/lib/useResolvedParams';

export default function GroupDetailClient() {
  const { id } = useResolvedParams<{ id: string }>('/groups/[id]');
  const router = useRouter();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [adminGroupId, setAdminGroupId] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const { data: group, isLoading } = useQuery({
    queryKey: ['group', id],
    queryFn: () => api.groups.get(id),
  });

  const { data: allGroups } = useQuery({
    queryKey: ['groups'],
    queryFn: () => api.groups.list(),
    enabled: editing || !!group?.admin_group_id,
  });

  const updateMutation = useMutation({
    mutationFn: () => api.groups.update(id, { name, description, admin_group_id: adminGroupId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['group', id] });
      queryClient.invalidateQueries({ queryKey: ['groups'] });
      setEditing(false);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => api.groups.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['groups'] });
      router.push('/groups');
    },
  });

  if (isLoading) return <p>Loading...</p>;
  if (!group) return <p>Group not found.</p>;

  const isAdmin = group.my_role === 'admin';

  return (
    <div>
      <div className="flex justify-between items-start mb-6">
        <div>
          {editing ? (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                updateMutation.mutate();
              }}
              className="space-y-3"
            >
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
                className="text-2xl font-bold border rounded px-2 py-1 w-full"
              />
              <textarea
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                rows={2}
                className="w-full border rounded px-2 py-1 text-sm"
              />
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">
                  Administrator Group
                </label>
                <select
                  value={adminGroupId ?? ''}
                  onChange={(e) => setAdminGroupId(e.target.value || null)}
                  className="w-full border rounded px-2 py-1.5 text-sm"
                >
                  <option value="">None</option>
                  {allGroups
                    ?.filter((g) => g.id !== id)
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
              <div className="flex gap-2">
                <button
                  type="submit"
                  disabled={updateMutation.isPending}
                  className="bg-blue-600 text-white px-3 py-1.5 text-sm rounded hover:bg-blue-700 disabled:opacity-50"
                >
                  Save
                </button>
                <button
                  type="button"
                  onClick={() => setEditing(false)}
                  className="text-sm text-gray-500 hover:text-gray-700 px-3 py-1.5"
                >
                  Cancel
                </button>
              </div>
            </form>
          ) : (
            <>
              <h1 className="text-2xl font-bold">{group.name}</h1>
              {group.description && (
                <p className="text-gray-500 mt-1">{group.description}</p>
              )}
              {group.admin_group_id && allGroups?.find((g) => g.id === group.admin_group_id) && (
                <p className="text-sm text-purple-600 mt-1">
                  Admin group:{' '}
                  <a href={`/groups/${group.admin_group_id}`} className="underline hover:text-purple-800">
                    {allGroups.find((g) => g.id === group.admin_group_id)!.name}
                  </a>
                </p>
              )}
            </>
          )}
        </div>
        {!editing && isAdmin && (
          <div className="flex gap-2">
            <button
              onClick={() => {
                setName(group.name);
                setDescription(group.description);
                setAdminGroupId(group.admin_group_id ?? null);
                setEditing(true);
              }}
              className="text-sm text-gray-600 hover:text-gray-900 px-3 py-1.5"
            >
              Edit
            </button>
          </div>
        )}
      </div>

      <GroupManager groupId={id} isAdmin={isAdmin} />

      {/* Danger zone — admin only */}
      {isAdmin && (
        <div className="border-t pt-4 mt-8">
          <h4 className="text-sm font-semibold text-red-600 mb-2">Danger Zone</h4>
          <p className="text-xs text-gray-500 mb-3">
            Deleting this group removes all members, invites, and access grants.
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
      )}
    </div>
  );
}
