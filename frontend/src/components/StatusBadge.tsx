type StatusBadgeProps = {
  value: string;
};

export function StatusBadge({ value }: StatusBadgeProps) {
  const normalized = value.toLowerCase();
  return <span className={`status-badge ${normalized}`}>{normalized === "blocked" ? "Blocked" : "Allowed"}</span>;
}
