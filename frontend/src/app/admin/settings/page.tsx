'use client';

import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api, BackupSettings, LLMProvider, DiscoveredModel } from '@/lib/api';

export default function AdminSettingsPage() {
  return (
    <div className="max-w-3xl space-y-8">
      <div>
        <h1 className="text-2xl font-bold">Settings</h1>
        <p className="text-sm text-gray-500">System configuration for authentication, analysis executor, and backups</p>
      </div>

      <OIDCConfigSection />
      <LLMProvidersSection />
      <ExecutorConfigSection />
      <BackupConfigSection />
    </div>
  );
}

function OIDCConfigSection() {
  const queryClient = useQueryClient();
  const { data: config, isLoading } = useQuery({
    queryKey: ['admin', 'oidc-config'],
    queryFn: api.admin.getOIDCConfig,
  });

  const [form, setForm] = useState<{
    oidc_issuer: string;
    oidc_client_id: string;
    oidc_client_secret: string;
  } | null>(null);
  const [showSecret, setShowSecret] = useState(false);

  const currentForm = form ?? {
    oidc_issuer: config?.oidc_issuer ?? '',
    oidc_client_id: config?.oidc_client_id ?? '',
    oidc_client_secret: '',
  };

  const updateMut = useMutation({
    mutationFn: (data: { oidc_issuer?: string; oidc_client_id?: string; oidc_client_secret?: string }) => {
      const payload: Record<string, string> = {};
      if (data.oidc_issuer) payload.oidc_issuer = data.oidc_issuer;
      if (data.oidc_client_id) payload.oidc_client_id = data.oidc_client_id;
      if (data.oidc_client_secret) payload.oidc_client_secret = data.oidc_client_secret;
      return api.admin.updateOIDCConfig(payload);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'oidc-config'] });
    },
  });

  const callbackUrl = typeof window !== 'undefined'
    ? `${window.location.origin}/api/v1/auth/oidc/callback`
    : '';

  if (isLoading) return <div className="text-gray-400 text-sm">Loading OIDC config...</div>;

  return (
    <div className="bg-white p-6 rounded-lg border space-y-4">
      <h2 className="font-semibold text-lg">OIDC Authentication</h2>
      <p className="text-sm text-gray-500">
        Configure OpenID Connect for production authentication.
      </p>

      {callbackUrl && (
        <div className="p-3 bg-blue-50 rounded-md">
          <div className="text-xs font-medium text-blue-700 mb-1">Callback URL</div>
          <code className="text-xs text-blue-900 break-all">{callbackUrl}</code>
          <p className="text-xs text-blue-600 mt-1">
            Register this URL with your OIDC provider as an allowed redirect URI.
          </p>
        </div>
      )}

      <form
        onSubmit={(e) => {
          e.preventDefault();
          updateMut.mutate(currentForm);
        }}
        className="space-y-4"
      >
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Issuer URL</label>
          <input
            type="url"
            value={currentForm.oidc_issuer}
            onChange={(e) =>
              setForm({ ...currentForm, oidc_issuer: e.target.value })
            }
            placeholder="https://cilogon.org"
            className="w-full border rounded-md px-3 py-2 text-sm"
          />
          {config?.oidc_issuer && (
            <p className="text-xs text-green-600 mt-0.5">Currently set: {config.oidc_issuer}</p>
          )}
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Client ID</label>
          <input
            type="text"
            value={currentForm.oidc_client_id}
            onChange={(e) =>
              setForm({ ...currentForm, oidc_client_id: e.target.value })
            }
            placeholder="your-client-id"
            className="w-full border rounded-md px-3 py-2 text-sm"
          />
          {config?.oidc_client_id && (
            <p className="text-xs text-green-600 mt-0.5">Currently set: {config.oidc_client_id}</p>
          )}
        </div>
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Client Secret</label>
          <div className="relative">
            <input
              type={showSecret ? 'text' : 'password'}
              value={currentForm.oidc_client_secret}
              onChange={(e) =>
                setForm({ ...currentForm, oidc_client_secret: e.target.value })
              }
              placeholder={config?.secret_set ? '••••• (already set, leave blank to keep)' : 'your-client-secret'}
              className="w-full border rounded-md px-3 py-2 text-sm pr-16"
            />
            <button
              type="button"
              onClick={() => setShowSecret(!showSecret)}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-xs text-gray-500 hover:text-gray-700"
            >
              {showSecret ? 'Hide' : 'Show'}
            </button>
          </div>
        </div>

        <div className="flex items-center gap-4">
          <button
            type="submit"
            disabled={updateMut.isPending}
            className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {updateMut.isPending ? 'Saving...' : 'Save OIDC Settings'}
          </button>
          {updateMut.isSuccess && <span className="text-green-600 text-sm">Saved!</span>}
          {updateMut.isError && (
            <span className="text-red-600 text-sm">Error: {updateMut.error?.message}</span>
          )}
        </div>
      </form>
    </div>
  );
}

const PROVIDER_PRESETS = [
  { value: 'anthropic', label: 'Anthropic', api_schema: 'anthropic', base_url: 'https://api.anthropic.com' },
  { value: 'openai', label: 'OpenAI', api_schema: 'openai', base_url: 'https://api.openai.com/v1' },
  { value: 'nrp', label: 'NRP (ACCESS)', api_schema: 'openai', base_url: 'https://ellm.nrp-nautilus.io/v1' },
  { value: 'custom', label: 'Custom', api_schema: 'openai', base_url: '' },
];

function LLMProvidersSection() {
  const queryClient = useQueryClient();
  const { data: providers, isLoading } = useQuery({
    queryKey: ['admin', 'llm-providers'],
    queryFn: api.llmProviders.list,
  });

  // Fetch env-based provider info from agent status.
  const { data: agentStatus } = useQuery({
    queryKey: ['agent-status'],
    queryFn: () => api.agent.status(),
    staleTime: 60_000,
  });
  const envProviders: { provider: string; api_schema: string; key_configured: boolean; base_url?: string; default_model?: string; enabled?: boolean }[] =
    (agentStatus as Record<string, unknown>)?.env_providers as typeof envProviders ?? [];

  const [showForm, setShowForm] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [preset, setPreset] = useState('anthropic');
  const [label, setLabel] = useState('');
  const [apiSchema, setApiSchema] = useState('anthropic');
  const [baseUrl, setBaseUrl] = useState('https://api.anthropic.com');
  const [defaultModel, setDefaultModel] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [enabled, setEnabled] = useState(true);
  const [discoveredModels, setDiscoveredModels] = useState<Record<string, DiscoveredModel[]>>({});
  const [discoveringId, setDiscoveringId] = useState<string | null>(null);

  const resetForm = () => {
    setShowForm(false);
    setEditingId(null);
    setPreset('anthropic');
    setLabel('');
    setApiSchema('anthropic');
    setBaseUrl('https://api.anthropic.com');
    setDefaultModel('');
    setApiKey('');
    setEnabled(true);
  };

  const applyPreset = (value: string) => {
    setPreset(value);
    const p = PROVIDER_PRESETS.find(pp => pp.value === value);
    if (p) {
      if (!editingId) setLabel(p.label);
      setApiSchema(p.api_schema);
      setBaseUrl(p.base_url);
    }
  };

  const startEdit = (p: LLMProvider) => {
    setEditingId(p.id);
    setShowForm(true);
    setLabel(p.label);
    setApiSchema(p.api_schema);
    setBaseUrl(p.base_url);
    setDefaultModel(p.default_model || '');
    setEnabled(p.enabled);
    setApiKey('');
    // Determine preset from existing values
    const match = PROVIDER_PRESETS.find(pp => pp.api_schema === p.api_schema && pp.base_url === p.base_url);
    setPreset(match?.value ?? 'custom');
  };

  const createMut = useMutation({
    mutationFn: () => api.llmProviders.create({ label, api_schema: apiSchema, base_url: baseUrl, default_model: defaultModel, api_key: apiKey, enabled }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'llm-providers'] });
      resetForm();
    },
  });

  const updateMut = useMutation({
    mutationFn: () =>
      api.llmProviders.update(editingId!, {
        label,
        api_schema: apiSchema,
        base_url: baseUrl,
        default_model: defaultModel,
        ...(apiKey ? { api_key: apiKey } : {}),
        enabled,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'llm-providers'] });
      resetForm();
    },
  });

  const deleteMut = useMutation({
    mutationFn: (id: string) => api.llmProviders.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'llm-providers'] });
    },
  });

  const toggleMut = useMutation({
    mutationFn: (p: LLMProvider) =>
      api.llmProviders.update(p.id, { label: p.label, api_schema: p.api_schema, base_url: p.base_url, default_model: p.default_model, enabled: !p.enabled }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'llm-providers'] });
    },
  });

  const toggleEnvMut = useMutation({
    mutationFn: ({ provider, currentEnabled }: { provider: string; currentEnabled: boolean }) => {
      const key = provider === 'anthropic' ? 'env_provider_anthropic_enabled' : 'env_provider_external_enabled';
      return api.admin.updateExecutorConfig({ [key]: currentEnabled ? 'false' : 'true' });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['agent-status'] });
      queryClient.invalidateQueries({ queryKey: ['available-providers'] });
    },
  });

  const handleDiscover = async (id: string) => {
    setDiscoveringId(id);
    try {
      const models = await api.llmProviders.discoverModels(id);
      setDiscoveredModels(prev => ({ ...prev, [id]: models }));
    } catch {
      setDiscoveredModels(prev => ({ ...prev, [id]: [] }));
    } finally {
      setDiscoveringId(null);
    }
  };

  const handleDiscoverEnv = async (envId: string) => {
    setDiscoveringId(envId);
    try {
      const models = await api.llmProviders.discoverEnvModels(envId);
      setDiscoveredModels(prev => ({ ...prev, [envId]: models }));
    } catch {
      setDiscoveredModels(prev => ({ ...prev, [envId]: [] }));
    } finally {
      setDiscoveringId(null);
    }
  };

  if (isLoading) return <div className="text-gray-400 text-sm">Loading LLM providers...</div>;

  return (
    <div className="bg-white p-6 rounded-lg border space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="font-semibold text-lg">LLM Providers</h2>
          <p className="text-sm text-gray-500">
            Configure global LLM providers. Enable/disable which providers are available for analyses.
          </p>
        </div>
        {!showForm && (
          <button
            onClick={() => { resetForm(); setShowForm(true); }}
            className="px-3 py-1.5 bg-blue-600 text-white text-sm rounded-md hover:bg-blue-700"
          >
            Add Provider
          </button>
        )}
      </div>

      {/* Environment-configured providers (read-only) */}
      {envProviders.length > 0 && (
        <div>
          <h3 className="text-xs font-medium text-gray-500 uppercase tracking-wide mb-2">From Environment</h3>
          <div className="border rounded-md divide-y bg-gray-50">
            {envProviders.map((ep) => {
              const envId = ep.provider === 'anthropic' ? 'env-anthropic' : 'env-external';
              return (
              <div key={ep.provider} className="p-3 flex items-center justify-between">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-sm">
                      {ep.provider === 'anthropic' ? 'Anthropic' : 'External LLM'}
                    </span>
                    <span className={`px-1.5 py-0.5 text-xs rounded ${
                      ep.api_schema === 'anthropic' ? 'bg-purple-100 text-purple-700' : 'bg-green-100 text-green-700'
                    }`}>
                      {ep.api_schema}
                    </span>
                    <span className="px-1.5 py-0.5 text-xs rounded bg-blue-100 text-blue-700">env</span>
                    {ep.key_configured ? (
                      <span className="text-xs text-green-600">Key configured</span>
                    ) : (
                      <span className="text-xs text-amber-600">No key</span>
                    )}
                  </div>
                  {ep.base_url && (
                    <div className="text-xs text-gray-400 truncate">{ep.base_url}</div>
                  )}
                  {ep.default_model && (
                    <div className="text-xs text-gray-500">Default model: {ep.default_model}</div>
                  )}
                  {discoveredModels[envId] && (
                    <div className="mt-1 text-xs text-gray-500">
                      {discoveredModels[envId].length} models: {discoveredModels[envId].slice(0, 5).map(m => m.display_name || m.id).join(', ')}
                      {discoveredModels[envId].length > 5 && ` (+${discoveredModels[envId].length - 5} more)`}
                    </div>
                  )}
                </div>
                <div className="flex items-center gap-2 ml-3">
                  {ep.key_configured && (
                    <button
                      onClick={() => handleDiscoverEnv(envId)}
                      disabled={discoveringId === envId}
                      className="text-xs text-blue-600 hover:underline disabled:opacity-50"
                    >
                      {discoveringId === envId ? 'Loading...' : 'Models'}
                    </button>
                  )}
                  <button
                    onClick={() => toggleEnvMut.mutate({ provider: ep.provider, currentEnabled: ep.enabled !== false })}
                    disabled={toggleEnvMut.isPending}
                    className={`px-2 py-1 text-xs rounded ${
                      ep.enabled !== false
                        ? 'bg-green-100 text-green-700 hover:bg-green-200'
                        : 'bg-gray-100 text-gray-500 hover:bg-gray-200'
                    }`}
                  >
                    {ep.enabled !== false ? 'Enabled' : 'Disabled'}
                  </button>
                </div>
              </div>
              );
            })}
          </div>
          <p className="text-xs text-gray-400 mt-1">
            Set via server environment variables. Toggle to enable/disable for analyses. Restart the server to change keys or endpoints.
          </p>
        </div>
      )}

      {/* DB-managed provider list */}
      {providers && providers.length > 0 && (
        <div>
          {envProviders.length > 0 && (
            <h3 className="text-xs font-medium text-gray-500 uppercase tracking-wide mb-2">Managed Providers</h3>
          )}
          <div className="border rounded-md divide-y">
          {providers.map((p) => (
            <div key={p.id} className="p-3 flex items-center justify-between">
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-sm">{p.label}</span>
                  <span className={`px-1.5 py-0.5 text-xs rounded ${
                    p.api_schema === 'anthropic' ? 'bg-purple-100 text-purple-700' : 'bg-green-100 text-green-700'
                  }`}>
                    {p.api_schema}
                  </span>
                  {p.key_hint && (
                    <span className="text-xs text-gray-400">Key: {p.key_hint}</span>
                  )}
                </div>
                {p.base_url && (
                  <div className="text-xs text-gray-400 truncate">{p.base_url}</div>
                )}
                {p.default_model && (
                  <div className="text-xs text-gray-500">Default model: {p.default_model}</div>
                )}
                {discoveredModels[p.id] && (
                  <div className="mt-1 text-xs text-gray-500">
                    {discoveredModels[p.id].length} models: {discoveredModels[p.id].slice(0, 5).map(m => m.display_name || m.id).join(', ')}
                    {discoveredModels[p.id].length > 5 && ` (+${discoveredModels[p.id].length - 5} more)`}
                  </div>
                )}
              </div>
              <div className="flex items-center gap-2 ml-3">
                <button
                  onClick={() => handleDiscover(p.id)}
                  disabled={discoveringId === p.id}
                  className="text-xs text-blue-600 hover:underline disabled:opacity-50"
                >
                  {discoveringId === p.id ? 'Loading...' : 'Models'}
                </button>
                <button
                  onClick={() => toggleMut.mutate(p)}
                  className={`px-2 py-1 text-xs rounded ${
                    p.enabled
                      ? 'bg-green-100 text-green-700 hover:bg-green-200'
                      : 'bg-gray-100 text-gray-500 hover:bg-gray-200'
                  }`}
                >
                  {p.enabled ? 'Enabled' : 'Disabled'}
                </button>
                <button
                  onClick={() => startEdit(p)}
                  className="text-xs text-gray-600 hover:text-gray-900"
                >
                  Edit
                </button>
                <button
                  onClick={() => {
                    if (confirm(`Delete provider "${p.label}"?`)) deleteMut.mutate(p.id);
                  }}
                  className="text-xs text-red-600 hover:text-red-800"
                >
                  Delete
                </button>
              </div>
            </div>
          ))}
          </div>
        </div>
      )}

      {providers && providers.length === 0 && envProviders.length === 0 && !showForm && (
        <div className="text-sm text-gray-400 text-center py-4 border rounded-md">
          No LLM providers configured. Add one to get started.
        </div>
      )}

      {/* Add/Edit form */}
      {showForm && (
        <div className="border rounded-md p-4 bg-gray-50 space-y-3">
          <h3 className="text-sm font-medium">{editingId ? 'Edit Provider' : 'Add Provider'}</h3>

          <div>
            <label className="block text-xs font-medium text-gray-600 mb-1">Preset</label>
            <select
              value={preset}
              onChange={(e) => applyPreset(e.target.value)}
              className="w-full border rounded-md px-3 py-2 text-sm bg-white"
            >
              {PROVIDER_PRESETS.map((p) => (
                <option key={p.value} value={p.value}>{p.label}</option>
              ))}
            </select>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">Label</label>
              <input
                type="text"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                placeholder="My Provider"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-gray-600 mb-1">API Schema</label>
              <select
                value={apiSchema}
                onChange={(e) => setApiSchema(e.target.value)}
                className="w-full border rounded-md px-3 py-2 text-sm bg-white"
              >
                <option value="anthropic">Anthropic</option>
                <option value="openai">OpenAI-compatible</option>
              </select>
            </div>
            <div className="sm:col-span-2">
              <label className="block text-xs font-medium text-gray-600 mb-1">Base URL</label>
              <input
                type="url"
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                placeholder="https://api.anthropic.com"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
            <div className="sm:col-span-2">
              <label className="block text-xs font-medium text-gray-600 mb-1">Default Model</label>
              <input
                type="text"
                value={defaultModel}
                onChange={(e) => setDefaultModel(e.target.value)}
                placeholder="Leave blank to auto-select"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
              <p className="text-xs text-gray-400 mt-0.5">Used when no model is chosen per-analysis. Use &quot;Models&quot; button to discover available models.</p>
            </div>
            <div className="sm:col-span-2">
              <label className="block text-xs font-medium text-gray-600 mb-1">
                API Key {editingId && <span className="text-gray-400">(leave blank to keep existing)</span>}
              </label>
              <input
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder={editingId ? '••••• (unchanged)' : 'sk-...'}
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
          </div>

          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
              className="rounded border-gray-300"
            />
            Enabled
          </label>

          <div className="flex items-center gap-2">
            <button
              onClick={() => editingId ? updateMut.mutate() : createMut.mutate()}
              disabled={createMut.isPending || updateMut.isPending || !label || (!editingId && !apiKey)}
              className="px-3 py-1.5 bg-blue-600 text-white text-sm rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {(createMut.isPending || updateMut.isPending) ? 'Saving...' : editingId ? 'Update' : 'Add'}
            </button>
            <button
              onClick={resetForm}
              className="px-3 py-1.5 border text-sm rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
            {(createMut.isError || updateMut.isError) && (
              <span className="text-red-600 text-xs">
                {(createMut.error || updateMut.error)?.message}
              </span>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

const EXECUTOR_MODES = [
  { value: 'local', label: 'Local (in-process)', desc: 'Fork/exec agent in the server process. Simple but non-persistent.' },
  { value: 'process', label: 'Process (detached daemon)', desc: 'Detached processes with flock-based liveness. Survives restarts.' },
  { value: 'kubernetes', label: 'Kubernetes', desc: 'Run analyses as Kubernetes jobs. Scalable and persistent.' },
];

const K8S_FIELDS = [
  { key: 'k8s_namespace', label: 'Namespace', placeholder: 'swamp' },
  { key: 'k8s_kubeconfig', label: 'Kubeconfig Path', placeholder: '/home/swamp/.kube/config', help: 'Path to kubeconfig file on the server. Leave empty for in-cluster service account credentials.' },
  { key: 'k8s_worker_image', label: 'Worker Image', placeholder: 'ghcr.io/org/swamp-worker:latest', hintKey: 'hint_server_image', hintLabel: 'server image' },
  { key: 'k8s_image_pull_secret', label: 'Image Pull Secret', placeholder: 'registry-credentials', hintKey: 'hint_image_pull_secret', hintLabel: 'env default' },
  { key: 'k8s_worker_service_account', label: 'Service Account', placeholder: 'swamp-worker' },
  { key: 'k8s_worker_cpu_request', label: 'CPU Request', placeholder: '500m' },
  { key: 'k8s_worker_cpu_limit', label: 'CPU Limit', placeholder: '2' },
  { key: 'k8s_worker_mem_request', label: 'Memory Request', placeholder: '512Mi' },
  { key: 'k8s_worker_mem_limit', label: 'Memory Limit', placeholder: '2Gi' },
  { key: 'k8s_worker_node_selector', label: 'Node Selector', placeholder: 'key=value,key2=value2' },
  { key: 'k8s_worker_tolerations', label: 'Tolerations', placeholder: 'key=value:effect,...' },
  { key: 'k8s_worker_labels', label: 'Pod Labels', placeholder: 'key=value,key2=value2' },
  { key: 'k8s_worker_annotations', label: 'Pod Annotations', placeholder: 'key=value,key2=value2' },
  { key: 'k8s_pod_ttl_seconds', label: 'Job TTL (seconds)', placeholder: '3600' },
];

function ExecutorConfigSection() {
  const queryClient = useQueryClient();
  const { data: config, isLoading } = useQuery({
    queryKey: ['admin', 'executor-config'],
    queryFn: api.admin.getExecutorConfig,
  });

  const [form, setForm] = useState<Record<string, string> | null>(null);

  const currentForm: Record<string, string> = form ?? {
    executor_mode: config?.executor_mode ?? '',
    max_concurrent_analyses: config?.max_concurrent_analyses ?? '',
    ...Object.fromEntries(K8S_FIELDS.map(f => [f.key, config?.[f.key] ?? ''])),
  };

  const activeMode = config?.active_mode ?? 'unknown';
  const selectedMode = currentForm.executor_mode || activeMode;

  const updateMut = useMutation({
    mutationFn: (data: Record<string, string>) => api.admin.updateExecutorConfig(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'executor-config'] });
    },
  });

  if (isLoading) return <div className="text-gray-400 text-sm">Loading executor config...</div>;

  return (
    <div className="bg-white p-6 rounded-lg border space-y-4">
      <h2 className="font-semibold text-lg">Analysis Executor</h2>
      <p className="text-sm text-gray-500">
        Configure how security analyses are executed. Changes take effect after server restart.
      </p>

      <div className="p-3 bg-gray-50 rounded-md">
        <div className="text-xs font-medium text-gray-500 mb-1">Currently Active</div>
        <div className="text-sm font-medium">
          {EXECUTOR_MODES.find(m => m.value === activeMode)?.label ?? activeMode}
        </div>
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          updateMut.mutate(currentForm);
        }}
        className="space-y-4"
      >
        {/* Executor mode */}
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-2">Executor Mode</label>
          <div className="space-y-2">
            {EXECUTOR_MODES.map((mode) => (
              <label
                key={mode.value}
                className={`flex items-start gap-3 p-3 border rounded-md cursor-pointer transition-colors ${
                  selectedMode === mode.value ? 'border-blue-500 bg-blue-50' : 'border-gray-200 hover:bg-gray-50'
                }`}
              >
                <input
                  type="radio"
                  name="executor_mode"
                  value={mode.value}
                  checked={selectedMode === mode.value}
                  onChange={(e) => setForm({ ...currentForm, executor_mode: e.target.value })}
                  className="mt-0.5"
                />
                <div>
                  <div className="text-sm font-medium">{mode.label}</div>
                  <div className="text-xs text-gray-500">{mode.desc}</div>
                </div>
              </label>
            ))}
          </div>
        </div>

        {/* Common settings */}
        <div className="border-t pt-4">
          <h3 className="text-sm font-medium text-gray-700 mb-2">General</h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Max Concurrent Analyses</label>
              <input
                type="number"
                min={1}
                value={currentForm.max_concurrent_analyses}
                onChange={(e) => setForm({ ...currentForm, max_concurrent_analyses: e.target.value })}
                placeholder="2"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
          </div>
        </div>

        {/* Kubernetes settings */}
        {selectedMode === 'kubernetes' && (
          <div className="border-t pt-4">
            <h3 className="text-sm font-medium text-gray-700 mb-2">Kubernetes Settings</h3>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              {K8S_FIELDS.map((field) => {
                const hint = field.hintKey ? config?.[field.hintKey] : undefined;
                const placeholder = hint || field.placeholder;
                return (
                  <div key={field.key}>
                    <label className="block text-sm font-medium text-gray-700 mb-1">{field.label}</label>
                    <input
                      type="text"
                      value={currentForm[field.key] ?? ''}
                      onChange={(e) => setForm({ ...currentForm, [field.key]: e.target.value })}
                      placeholder={placeholder}
                      className="w-full border rounded-md px-3 py-2 text-sm"
                    />
                    {hint && !currentForm[field.key] && (
                      <button
                        type="button"
                        className="text-xs text-blue-600 hover:underline mt-0.5"
                        onClick={() => setForm({ ...currentForm, [field.key]: hint })}
                      >
                        Use {field.hintLabel}: {hint}
                      </button>
                    )}
                    {field.help && (
                      <p className="text-xs text-gray-400 mt-0.5">{field.help}</p>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {selectedMode !== activeMode && (
          <div className="p-3 bg-amber-50 border border-amber-200 rounded-md">
            <p className="text-sm text-amber-800">
              Changing the executor mode requires a server restart to take effect.
            </p>
          </div>
        )}

        <div className="flex items-center gap-4">
          <button
            type="submit"
            disabled={updateMut.isPending}
            className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {updateMut.isPending ? 'Saving...' : 'Save Executor Settings'}
          </button>
          {updateMut.isSuccess && <span className="text-green-600 text-sm">Saved!</span>}
          {updateMut.isError && (
            <span className="text-red-600 text-sm">Error: {updateMut.error?.message}</span>
          )}
        </div>
      </form>
    </div>
  );
}

function BackupConfigSection() {
  const queryClient = useQueryClient();
  const { data: settings, isLoading } = useQuery({
    queryKey: ['admin', 'backup-settings'],
    queryFn: api.admin.getBackupSettings,
  });

  const [form, setForm] = useState<BackupSettings | null>(null);

  const currentForm: BackupSettings = form ?? {
    backup_frequency_hours: settings?.backup_frequency_hours ?? 0,
    backup_bucket: settings?.backup_bucket ?? '',
    backup_endpoint: settings?.backup_endpoint ?? '',
    backup_access_key: settings?.backup_access_key ?? '',
    backup_secret_key: settings?.backup_secret_key ?? '',
    backup_use_ssl: settings?.backup_use_ssl ?? true,
  };

  const updateMut = useMutation({
    mutationFn: (data: BackupSettings) => api.admin.updateBackupSettings(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'backup-settings'] });
    },
  });

  if (isLoading) return <div className="text-gray-400 text-sm">Loading backup config...</div>;

  return (
    <div className="bg-white p-6 rounded-lg border space-y-4">
      <h2 className="font-semibold text-lg">Automated Backups</h2>
      <p className="text-sm text-gray-500">
        Configure automatic backup schedule and optional alternate S3 storage.
        Backups are encrypted with a key derived from the master key.
      </p>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          updateMut.mutate(currentForm);
        }}
        className="space-y-4"
      >
        <div>
          <label className="block text-sm font-medium text-gray-700 mb-1">Backup Frequency (hours)</label>
          <input
            type="number"
            min={0}
            step={1}
            value={currentForm.backup_frequency_hours}
            onChange={(e) =>
              setForm({
                ...currentForm,
                backup_frequency_hours: parseInt(e.target.value) || 0,
              })
            }
            className="w-full border rounded-md px-3 py-2 text-sm"
          />
          <p className="text-xs text-gray-400 mt-0.5">
            Set to 0 to disable automatic backups. Recommended: 24 (daily).
          </p>
        </div>

        <div className="border-t pt-4">
          <h3 className="text-sm font-medium text-gray-700 mb-2">Alternate Backup S3 (optional)</h3>
          <p className="text-xs text-gray-400 mb-3">
            Leave empty to store backups in the default S3 bucket.
          </p>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Endpoint</label>
              <input
                type="text"
                value={currentForm.backup_endpoint}
                onChange={(e) =>
                  setForm({ ...currentForm, backup_endpoint: e.target.value })
                }
                placeholder="s3.amazonaws.com"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Bucket</label>
              <input
                type="text"
                value={currentForm.backup_bucket}
                onChange={(e) =>
                  setForm({ ...currentForm, backup_bucket: e.target.value })
                }
                placeholder="my-backup-bucket"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Access Key</label>
              <input
                type="text"
                value={currentForm.backup_access_key}
                onChange={(e) =>
                  setForm({ ...currentForm, backup_access_key: e.target.value })
                }
                placeholder="Leave empty to use default"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-gray-700 mb-1">Secret Key</label>
              <input
                type="password"
                value={currentForm.backup_secret_key}
                onChange={(e) =>
                  setForm({ ...currentForm, backup_secret_key: e.target.value })
                }
                placeholder="Leave empty to use default"
                className="w-full border rounded-md px-3 py-2 text-sm"
              />
            </div>
          </div>
          <div className="mt-3">
            <label className="flex items-center gap-2 text-sm text-gray-700">
              <input
                type="checkbox"
                checked={currentForm.backup_use_ssl}
                onChange={(e) =>
                  setForm({ ...currentForm, backup_use_ssl: e.target.checked })
                }
                className="rounded border-gray-300"
              />
              Use SSL/TLS
            </label>
          </div>
        </div>

        <div className="flex items-center gap-4">
          <button
            type="submit"
            disabled={updateMut.isPending}
            className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {updateMut.isPending ? 'Saving...' : 'Save Backup Settings'}
          </button>
          {updateMut.isSuccess && <span className="text-green-600 text-sm">Saved!</span>}
          {updateMut.isError && (
            <span className="text-red-600 text-sm">Error: {updateMut.error?.message}</span>
          )}
        </div>
      </form>
    </div>
  );
}
