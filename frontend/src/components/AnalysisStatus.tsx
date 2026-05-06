const statusConfig: Record<
  string,
  { label: string; color: string; bg: string }
> = {
  pending: { label: 'Pending', color: 'text-yellow-800', bg: 'bg-yellow-100' },
  running: { label: 'Running', color: 'text-blue-800', bg: 'bg-blue-100' },
  importing: { label: 'Importing', color: 'text-purple-800', bg: 'bg-purple-100' },
  completed: { label: 'Completed', color: 'text-green-800', bg: 'bg-green-100' },
  failed: { label: 'Failed', color: 'text-red-800', bg: 'bg-red-100' },
  timed_out: { label: 'Timed Out', color: 'text-orange-800', bg: 'bg-orange-100' },
  cancelled: { label: 'Cancelled', color: 'text-gray-800', bg: 'bg-gray-100' },
};

export function AnalysisStatus({
  status,
  className = '',
}: {
  status: string;
  className?: string;
}) {
  const config = statusConfig[status] || {
    label: status,
    color: 'text-gray-800',
    bg: 'bg-gray-100',
  };

  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${config.bg} ${config.color} ${className}`}
    >
      {(status === 'running' || status === 'importing') && (
        <span className="mr-1 h-2 w-2 rounded-full bg-current animate-pulse" />
      )}
      {config.label}
    </span>
  );
}
