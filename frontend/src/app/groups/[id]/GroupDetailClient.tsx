'use client';

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useParams, useRouter } from 'next/navigation';
import { useState } from 'react';
import { GroupManager } from '@/components/GroupManager';

export default function GroupDetailClient() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [adminGroupId, setAdminGroupId] = useState<string | null>(null);

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
                  className="bg-blue-600 text-white px-3 py-1 text-sm rounded"
                >
                  Save
                </button>
                <button
                  type="button"
                  onClick={() => setEditing(false)}
                  className="px-3 py-1 text-sm border rounded"
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
              {group.admin_group_id && (
                <p className="text-sm text-purple-600 mt-1">
                  Admin group:{' '}
                  <a href={`/groups/${group.admin_group_id}`} className="underline hover:text-purple-800">
                    {allGroups?.find((g) => g.id === group.admin_group_id)?.name ?? group.admin_group_id.slice(0, 8)}
                  </a>
                </p>
              )}
            </>
          )}
        </div>
        {!editing && (
          <div className="flex gap-2">
            <button
              onClick={() => {
                setName(group.name);
                setDescription(group.description);
                setAdminGroupId(group.admin_group_id ?? null);
                setEditing(true);
              }}
              className="px-3 py-1.5 text-sm border rounded hover:bg-gray-50"
            >
              Edit
            </button>
            <button
              onClick={() => {
                if (confirm('Delete this group?')) {
                  deleteMutation.mutate();
                }
              }}
              className="px-3 py-1.5 text-sm text-red-600 border border-red-200 rounded hover:bg-red-50"
            >
              Delete
            </button>
          </div>
        )}
      </div>

      <GroupManager groupId={id} />
    </div>
  );
}
